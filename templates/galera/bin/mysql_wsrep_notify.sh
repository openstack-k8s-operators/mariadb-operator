#!/bin/bash

# NOTE(dciabrin) we might use downward API to populate those in the future
PODNAME=$HOSTNAME
SERVICE=${PODNAME/-galera-[0-9]*/}

# API server config
APISERVER=https://kubernetes.default.svc
SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount
NAMESPACE=$(cat ${SERVICEACCOUNT}/namespace)
TOKEN=$(cat ${SERVICEACCOUNT}/token)
CACERT=${SERVICEACCOUNT}/ca.crt

# Retry config
RETRIES=6
WAIT=1


##
## Utilities functions
##
## NOTE: mysql diverts this script's stdout, but stderr is logged to the
## configured log-error file (e.g. /var/log/mariadb/mariadb.log)
function log() {
    echo "$(date +%F_%H_%M_%S) `basename $0` $*" >&2
}

function log_error() {
    echo "$(date +%F_%H_%M_%S) `basename $0` ERROR: $*" >&2
}

function mysql_get_status {
    local name=$1
    mysql -nNE -uroot -p"${DB_ROOT_PASSWORD}" -e "show status like '${name}';" | tail -1
    local rc=$?
    [ $rc = 0 ] || log_error "could not get value of mysql variable '${name}' (rc=$rc)"
}

function mysql_get_members {
    mysql -nN -uroot -p"${DB_ROOT_PASSWORD}" -e "select node_name from mysql.wsrep_cluster_members;"
    local rc=$?
    [ $rc = 0 ] || log_error "could not get cluster members from mysql' (rc=$rc)"
}

# When optional script parameters are not provided, set up the environment
# variables with the latest WSREP state retrieved from mysql
function mysql_probe_state {
    [ "$1" = "reprobe" ] && unset UUID PARTITION INDEX SIZE MEMBERS
    : ${UUID=$(mysql_get_status wsrep_gcomm_uuid)}
    : ${PARTITION=$(mysql_get_status wsrep_cluster_status)}
    : ${INDEX=$(mysql_get_status wsrep_local_index)}
    : ${SIZE=$(mysql_get_status wsrep_cluster_size)}
    : ${MEMBERS=$(mysql_get_members)}
    [ -n "${UUID}" -a -n "${PARTITION}" -a -n "${INDEX}" -a -n "${SIZE}" -a -n "${MEMBERS}" ]
}

# REST API call to the k8s API server
function api_server {
    local request=$1
    local service=$2
    # NOTE: a PUT request to the API server is basically a conditional write,
    # it only succeeds if no other write have been done on the CR in the mean time,
    # (i.e. if the timestamp of the JSON that is being sent to the API server matches
    # the timestamp of the service CR in the cluster)
    if [ "$request" = "PUT" ]; then
        request="$request -d @-"
    fi
    local output
    output=$(curl -s --cacert ${CACERT} --header "Content-Type:application/json" --header "Authorization: Bearer ${TOKEN}" --request $request ${APISERVER}/api/v1/namespaces/${NAMESPACE}/services/${service})

    local rc=$?
    if [ $rc != 0 ]; then
        log_error "call to API server failed for service ${service} (rc=$rc)"
        return 1
    fi
    if echo "${output}" | grep -q '"status": "Failure"'; then
        message=$(echo "${output}" | parse_output '["message"]')
        code=$(echo "${output}" | parse_output '["code"]')
        if [ "${code}" = 401 ]; then
            # Unauthorized means the token is no longer valid as the galera
            # resource is in the process of being deleted.
            return 2
        fi
        log_error "API server returned an error for service ${SERVICE}: ${message} (code=${code})"
        return 1
    fi
    echo "${output}"
    return 0
}

# Update the service's active endpoint
# (parse JSON with python3 as we don't have jq in the container image)
function service_endpoint {
    local endpoint="$1"
    # note: empty endpoint means "block incoming traffic", so the selector must still
    # be present, otherwise k8s would balance incoming traffic to _any_ available pod.
    python3 -c 'import json,sys;s=json.load(sys.stdin);s["spec"]["selector"]["statefulset.kubernetes.io/pod-name"]="'${endpoint}'";print(json.dumps(s,indent=2))'
    [ $? == 0 ] || log_error "Could not parse json endpoint (rc=$?)"
}

# retrieve data from a JSON structure
# (parse JSON with python3 as we don't have jq in the container image)
function parse_output {
    local key=$1
    python3 -c 'import json,sys;s=json.load(sys.stdin);print(s'${key}')'
    [ $? == 0 ] || log_error "Could not parse json endpoint (rc=$?)"
}

# Generic retry logic for an action function
function retry {
    local action=$1
    local retries=$RETRIES
    local wait=$WAIT
    local rc=1

    $action
    rc=$?
    while [ $rc -ne 0 -a $retries -gt 0 ]; do
        # if API call are unauthorized, the resource is being deleted
        # exit now as there is nothing more to do
        if [ $rc -eq 2 ]; then
            log "galera resource is being deleted, exit now."
            return 0
        fi
        log_error "previous action failed, retrying."
        sleep $wait
        $action
        rc=$?
        retries=$((retries - 1))
        # reprobe mysql state now, as if the cluster state changed since
        # the start of this script, we might not need to retry the action
        mysql_probe_state reprobe
    done
    if [ $rc -ne 0 ]; then
        log_error "Could not run action after ${RETRIES} tries. Stop retrying."
    fi
    return $rc
}


##
## Actions
##

## Change the current Active endpoint in a service
function reconfigure_service_endpoint {
    if [ $PARTITION != "Primary" -o "$INDEX" != "0" ]; then
        log "Node ${PODNAME} is not the first member of a Primary partion (index: ${INDEX}). Exiting"
        return 0
    fi

    CURRENT_SVC=$(api_server GET "$SERVICE")
    local rc=$?
    [ $rc == 0 ] || return $rc

    CURRENT_ENDPOINT=$(echo "$CURRENT_SVC" | parse_output '["spec"]["selector"].get("statefulset.kubernetes.io/pod-name","")')
    [ $? == 0 ] || return 1
    # do not reconfigure endpoint if unecessary, to avoid client disconnections
    if [ -n "${CURRENT_ENDPOINT}" ] && echo "$MEMBERS" | grep -q "^${CURRENT_ENDPOINT}\$"; then
        log "Active endpoint ${CURRENT_ENDPOINT} is still part of the primary partition. Nothing to be done."
        return 0
    fi
    if [ "${CURRENT_ENDPOINT}" == "${PODNAME}" ]; then
        log "Node ${PODNAME} is currently the active endpoint for service ${SERVICE}. Nothing to be done."
        return 0
    fi

    NEW_SVC=$(echo "$CURRENT_SVC" | service_endpoint "$PODNAME")
    [ $? == 0 ] || return 1

    log "Setting ${PODNAME} as the new active endpoint for service ${SERVICE}"
    UPDATE_RESULT=$(echo "$NEW_SVC" | api_server PUT "$SERVICE")
    [ $? == 0 ] || return 1

    return 0
}

## Failover to another node if we are the current Active endpoint
function failover_service_endpoint {
    if [ $PARTITION != "Primary" ]; then
        log "Node ${PODNAME} is not the Primary partion. Nothing to be done."
        return 0
    fi

    CURRENT_SVC=$(api_server GET "$SERVICE")
    local rc=$?
    [ $rc == 0 ] || return $rc

    CURRENT_ENDPOINT=$(echo "$CURRENT_SVC" | parse_output '["spec"]["selector"].get("statefulset.kubernetes.io/pod-name","")')
    [ $? == 0 ] || return 1
    if [ "${CURRENT_ENDPOINT}" != "${PODNAME}" ]; then
        log "Node ${PODNAME} is not the active endpoint. Nothing to be done."
        return 0
    fi
    # select the first available node in the primary partition to be the failover endpoint
    NEW_ENDPOINT=$(echo "$MEMBERS" | grep -v "${PODNAME}" | head -1)
    if [ -z "${NEW_ENDPOINT}" ]; then
        log "No other available node to become the active endpoint."
    fi

    NEW_SVC=$(echo "$CURRENT_SVC" | service_endpoint "$NEW_ENDPOINT")
    [ $? == 0 ] || return 1

    log "Configuring a new active endpoint for service ${SERVICE}: '${CURRENT_ENDPOINT}' -> '${NEW_ENDPOINT}'"
    UPDATE_RESULT=$(echo "$NEW_SVC" | api_server PUT "$SERVICE")
    [ $? == 0 ] || return 1

    return 0
}

## Change the Active endpoint from the service
function remove_service_endpoint {
    CURRENT_SVC=$(api_server GET "$SERVICE")
    local rc=$?
    [ $rc == 0 ] || return $rc

    CURRENT_ENDPOINT=$(echo "$CURRENT_SVC" | parse_output '["spec"]["selector"].get("statefulset.kubernetes.io/pod-name","")')
    [ $? == 0 ] || return 1
    if [ "${CURRENT_ENDPOINT}" != "${PODNAME}" ]; then
        log "Node ${PODNAME} is currently not the active endpoint for service ${SERVICE}. Nothing to be done."
        return 0
    fi

    NEW_SVC=$(echo "$CURRENT_SVC" | service_endpoint "")
    [ $? == 0 ] || return 1

    log "Removing ${PODNAME} endpoint from service ${SERVICE}"
    UPDATE_RESULT=$(echo "$NEW_SVC" | api_server PUT "$SERVICE")
    [ $? == 0 ] || return 1

    return 0
}



## Main

log "called with args: $*"

# Galera always calls script with --status argument
# All other optional arguments (uuid,partition,index...):
# UUID: cluster's current UUID
# MEMBERS: galera node connected to the cluster
# SIZE: number of nodes in the cluster
# INDEX: member index in the cluster
# PARTITION: cluster partition we're in (Primary, Non-primary)
while [ $# -gt 0 ]; do
    case $1 in
        --status)
            STATUS=$2
            shift;;
        --members)
            MEMBERS=$(echo "$2" | tr ',' '\n' | cut -d/ -f2)
            SIZE=$(echo "$MEMBERS" | wc -l)
            shift;;
        --primary)
            [ "$2" = "yes" ] && PARTITION="Primary"
            [ "$2" = "no" ] && PARTITION="Non-primary"
            shift;;
        --index)
            INDEX=$2
            shift;;
        --uuid)
            shift;;
    esac
    shift
done

if [ -z "${STATUS}" ]; then
    log_error called without --status STATUS
    exit 1
fi

# Contition: ask for a failover. This should be called when mysql is running
if echo "${STATUS}" | grep -i -q -e 'failover'; then
    mysql_probe_state
    if [ $? != 0 ]; then
        log_error "Could not probe missing mysql information. Aborting"
    fi
    retry "failover_service_endpoint"
fi

# Condition: disconnecting -> remove oneself from endpoint if Active
if echo "${STATUS}" | grep -i -q -e 'disconnecting'; then
    retry "remove_service_endpoint"
    exit $?
fi

# Conditions that do not require endpoint updates
if echo "${STATUS}" | grep -i -q -v -e 'synced'; then
    exit 0
fi

# At this point mysql is started, query missing arguments
mysql_probe_state
if [ $? != 0 ]; then
    log_error "Could not probe missing mysql information. Aborting"
fi

# Condition: first member of the primary partition -> set as Active endpoint
if [ $PARTITION = "Primary" -a $SIZE -ge 0 -a "$INDEX" = "0" ]; then
    retry "reconfigure_service_endpoint"
    exit $?
fi
