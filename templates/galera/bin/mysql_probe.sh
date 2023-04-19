#!/bin/bash
set -eu

# This secret is mounted by k8s and always up to date
read -s -u 3 3< /var/lib/secrets/dbpassword MYSQL_PWD || true
export MYSQL_PWD

PROBE_USER=root

# Consider the pod has "started" once mysql is reachable
if [ "$1" = "startup" ]; then
    mysql -u${PROBE_USER} -sNe "select(1);"
    exit $?
fi

set -x

case "$1" in
    readiness)
        # If the node is e.g. a donor, it cannot serve traffic
        mysql -u${PROBE_USER} -sNe "show status like 'wsrep_local_state_comment';" | grep -w -e Synced;;
    liveness)
        # If the node is not in the primary partition, restart it
        mysql -u${PROBE_USER} -sNe "show status like 'wsrep_cluster_status';" | grep -w -e Primary;;
    *)
        echo "Invalid probe option '$1'"
        exit 1;;
esac
