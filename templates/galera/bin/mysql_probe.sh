#!/bin/bash
set -u

# This secret is mounted by k8s and always up to date
read -s -u 3 3< <(cat /var/lib/secrets/dbpassword; echo) MYSQL_PWD
export MYSQL_PWD

PROBE_USER=root

MYSQL_SOCKET=/var/lib/mysql/mysql.sock
SST_IN_PROGRESS=/var/lib/mysql/sst_in_progress

CHECK_RETRY=10
CHECK_WAIT=0.5
STARTUP_WAIT=2

LAST_STATE=""
function log_state {
    local state="$1"
    # do not duplicate error logs in the probe, to minimize the
    # output in k8s events in case the probe fails
    if [ "${LAST_STATE}" != "${state}" ]; then
        LAST_STATE="${state}"
    fi
}

function log_last_state {
    if [ -n "${LAST_STATE}" ]; then
        echo "${LAST_STATE}"
    fi
}
trap log_last_state EXIT

function get_mysql_status {
    local status=$1
    local i
    local out
    for i in $(seq $CHECK_RETRY); do
        out=$(mysql -u${PROBE_USER} -sNEe "show status like '${status}';" 2>&1)
        if [ $? -eq 0 ]; then
            echo "${out}" | tail -1
            return 0
        else
            sleep ${CHECK_WAIT}
        fi
    done
    # if we pass here, log the last error from mysql
    echo "${out}" >&2
    return 1
}

function check_mysql_status {
    local status=$1
    local expect=$2
    local val
    local rc

    val=$(get_mysql_status "${status}")
    test "${val}" = "${expect}"
    rc=$?
    if [ $rc -ne 0 ]; then
        log_state "${status} (${val}) differs from ${expect}"
    fi
    return $rc
}

function check_sst_in_progress {
    local i
    # retry to give some time to mysql to set up the SST
    for i in $(seq $CHECK_RETRY); do
        if [ -e ${MYSQL_SOCKET} ]; then
            return 1
        elif [ -e ${SST_IN_PROGRESS} ]; then
            return 0
        else
            sleep ${CHECK_WAIT}
        fi
    done
    return 1
}

function check_mysql_ready {
    local i
    # retry to give some time to mysql to create its socket
    for i in $(seq $CHECK_RETRY); do
        if [ -e ${MYSQL_SOCKET} ] && mysqladmin -s -u${PROBE_USER} ping >dev/null; then
            return 0
        else
            sleep ${CHECK_WAIT}
        fi
    done
    return 1
}

# Monitor the startup sequence until the galera node is connected
# to a primary component and synced
# NOTE: as of mariadb 10.5, if mysql connects to a non-primary
# partition, it never creates any socket and gets stuck indefinitely.
# In that case, in order to not wait until the startup times out
# (very long), we error out of the probe so that the pod can restart
# and mysql reconnect to a primary partition if possible.
function check_mysql_startup {
    # mysql initialization sequence:
    #   . mysql connects to a remote galera node over port 4567
    #   . mysql optionally runs a SST (port 4444), SST marker created on disk
    #   . only at this point, InnoDB is initialized, mysql pidfile and
    #     mysql socket are created on disk

    if pgrep -f detect_gcomm_and_start.sh >/dev/null ; then
        log_state "waiting for gcomm URI"
        return 1
    fi
    # pidfile is not written on disk until mysql is ready,
    # so look for the mysqld process instead
    if ! pgrep -f /usr/libexec/mysqld >/dev/null ; then
        log_state "waiting for mysql to start"
        return 1
    fi

    # a bootstrap node must be reachable from the CLI to finish startup
    if pgrep -f -- '--wsrep-cluster-address=gcomm://(\W|$)' >/dev/null; then
        check_mysql_ready
        return $?
    # a joiner node must have an established socket connection before testing further
    elif pgrep -f -- '--wsrep-cluster-address=gcomm://\w' >/dev/null; then
        local connections
        connections=$(ss -tnH state established src :4567 or dst :4567 | wc -l)
        if ! test "${connections}" -ge 0; then
            log_state "waiting for mysql to join a galera cluster"
            return 1
        fi
    else
        log_state "could not determine galera startup mode"
        exit 1
    fi

    # a joiner node requires additional startup checks
    if [ -e /var/lib/mysql/mysql.sock ]; then
        # good case, mysql is ready to be probed from the CLI
        # check WSREP status like the regular liveness probe
        local status
        local comment
        status=$(get_mysql_status wsrep_cluster_status)
        comment=$(get_mysql_status wsrep_local_state_comment)
        if [ "${status}" = "Primary" -a "${comment}" = "Synced" ]; then
            return 0
        elif [ "${status}" = "Primary" ]; then
            log_state "waiting to be synced with the cluster"
            return 1
        elif [ "${status}" = "Non-primary" -a "${comment}" = "Synced"]; then
            log_state "mysql is connected to a non-primary partition, server stopped"
            exit 1
        else
            log_state "waiting for connection to a primary partition"
            return 1
        fi
    else
        # if there is no socket, mysql may be running an SST...
        if check_sst_in_progress; then
            log_state "waiting for SST to finish"
            return 1
        fi

        # ... if no SST was detected, it may have finished before
        # we probed it. Check a last time whether we can connect to mysql
        if check_mysql_ready; then
            return 0
        fi

        # At this stage, mysql is either trying to connect to a boostrap node
        # that resolved to an old pod IP, or it is is connected to a
        # non-primary partition. Either way, this is not recoverable, so
        # make the probe fail and let k8s kill the mysql server.

        log_state "could not find a primary partition to connect to"
        exit 1
    fi
    return 1
}


# startup probe loops until the node started or joined a galera cluster
# readiness and liveness probes are run by k8s only after start probe succeeded

case "$1" in
    startup)
        if [ -z "$2" ]; then
            echo "startup timeout option missing"
            exit 1
        fi
        TIME_TIMEOUT=$2

        # Run the entire check in a single startup probe to avoid spurious
        # "Unhealthy" k8s events to be logged. The probe stops in error
        # if the startup timeout is reached
        rc=1
        while [ $rc -ne 0 ]; do
            if check_mysql_startup; then
                exit 0
            else
                sleep ${STARTUP_WAIT};
                [ $SECONDS -ge $TIME_TIMEOUT ] && exit 1
            fi
        done
        exit $rc
        ;;
    readiness)
        # If the node is e.g. a donor, it cannot serve traffic
        check_mysql_status wsrep_local_state_comment Synced
        ;;
    liveness)
        # If the node is not in the primary partition, the failed liveness probe
        # will make k8s restart this pod
        check_mysql_status wsrep_cluster_status Primary
        ;;
    *)
        echo "Invalid probe option '$1'"
        exit 1;;
esac
