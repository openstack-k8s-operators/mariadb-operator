#!/bin/bash
set -u

# This secret is mounted by k8s and always up to date
read -s -u 3 3< /var/lib/secrets/dbpassword MYSQL_PWD || true
export MYSQL_PWD

PROBE_USER=root
function mysql_status_check {
    local status=$1
    local expect=$2
    set -x
    mysql -u${PROBE_USER} -sNEe "show status like '${status}';" | tail -1 | grep -w -e "${expect}"
}

# Consider the pod has "started" once mysql is reachable
# and is part of the primary partition
if [ "$1" = "startup" ]; then
    mysql_status_check wsrep_cluster_status Primary
    exit $?
fi

# readiness and liveness probes are run by k8s only after start probe succeeded

case "$1" in
    readiness)
        # If the node is e.g. a donor, it cannot serve traffic
        mysql_status_check wsrep_local_state_comment Synced
        ;;
    liveness)
        # If the node is not in the primary partition, the failed liveness probe
        # will make k8s restart this pod
        mysql_status_check wsrep_cluster_status Primary
        ;;
    *)
        echo "Invalid probe option '$1'"
        exit 1;;
esac
