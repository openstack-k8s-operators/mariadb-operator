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
function log() {
    echo "$(date +%F_%H_%M_%S) `basename $0` $*"
}

function log_error() {
    echo "$(date +%F_%H_%M_%S) `basename $0` ERROR: $*" >&2
}

function mysql_get_status {
    local name=$1
    mysql -nNE -uroot -p"${DB_ROOT_PASSWORD}" -e "show status like '${name}';" | tail -1
    if [ $? != 0 ]; then
        log_error "could not get value of mysql variable '${name}' (rc=$?)"
        return 1
    fi
}

# Refresh environment variables with the latest WSREP state from mysql
function mysql_probe_state {
    UUID=$(mysql_get_status wsrep_gcomm_uuid)
    PARTITION=$(mysql_get_status wsrep_cluster_status)
    INDEX=$(mysql_get_status wsrep_local_index)
    SIZE=$(mysql_get_status wsrep_cluster_size)
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
        log_error "API server returned an error for service ${SERVICE}: ${message} (code=${code})"
        return 1
    fi
    echo "${output}"
    return 0
}

# Update the service's active endpoint
# (parse JSON with python3 as we don't have jq in the container image)
function service_endpoint {
    local endpoint=$1
    if [ -n "${endpoint}" ]; then
        python3 -c 'import json,sys;s=json.load(sys.stdin);s["spec"]["selector"]["statefulset.kubernetes.io/pod-name"]="'${endpoint}'";print(json.dumps(s,indent=2))'
    else
        python3 -c 'import json,sys;s=json.load(sys.stdin);s["spec"]["selector"].pop("statefulset.kubernetes.io/pod-name", None);print(json.dumps(s,indent=2))'
    fi
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
        log_error "previous action failed, retrying."
        sleep $wait
        $action
        rc=$?
        retries=$((retries - 1))
        # reprobe mysql state now, as if the cluster state changed since
        # the start of this script, we might not need to retry the action
        mysql_probe_state
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
    [ $? == 0 ] || return 1

    CURRENT_ENDPOINT=$(echo "$CURRENT_SVC" | parse_output '["spec"]["selector"].get("statefulset.kubernetes.io/pod-name","")')
    [ $? == 0 ] || return 1
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

## Change the Active endpoint from the service
function remove_service_endpoint {
    CURRENT_SVC=$(api_server GET "$SERVICE")
    [ $? == 0 ] || return 1

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

# mysql diverts this script's stdout/stderr, so in order for its output
# to be logged properly, reuse dumb-init's stdout
exec &> >(tee -a /proc/1/fd/1) 2>&1
log "called with args: $*"

# Galera always calls script with --status argument
# All other arguments (uuid,partition,index...) are optional,
# so get those values by probing mysql directly
STATUS=""
PARTITION=""
INDEX=""
while [ $# -gt 0 ]; do
    case $1 in
        --status)
            STATUS=$2
            shift;;
        --uuid|--members|--primary|--index)
            shift;;
    esac
    shift
done

if [ -z "${STATUS}" ]; then
    log_error called without --status STATUS
    exit 1
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

# Condition: first member of the primary partition -> set as Active endpoint
if [ $PARTITION = "Primary" -a $SIZE -ge 0 -a "$INDEX" = "0" ]; then
    retry "reconfigure_service_endpoint"
    exit $?
fi
