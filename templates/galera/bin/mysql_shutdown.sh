#!/bin/bash

# NOTE(dciabrin) we might use downward API to populate those in the future
PODNAME=$HOSTNAME
SERVICE=${PODNAME/-galera-[0-9]*/}
MYSQL_SOCKET=/var/lib/mysql/mysql.sock

# API server config
APISERVER=https://kubernetes.default.svc
SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount
NAMESPACE=$(cat ${SERVICEACCOUNT}/namespace)
TOKEN=$(cat ${SERVICEACCOUNT}/token)
CACERT=${SERVICEACCOUNT}/ca.crt

function log() {
    echo "$(date +%F_%H_%M_%S) `basename $0` $*"
}

# Log in mariadb's log file if configured, so the output of this script
# is captured when logToDisk is enabled in the galera CR
LOGFILE=$(my_print_defaults mysqld | grep log-error | cut -d= -f2)
if [ -f "$LOGFILE" ]; then
    exec &> >(cat >> "$LOGFILE") 2>&1
else
    exec &> >(cat >> /proc/1/fd/1) 2>&1
fi

# if the mysql socket is not available, mysql is either not started or
# not reachable, orchestration stops here.
if [ ! -e $MYSQL_SOCKET ]; then
    exit 0
fi

# On update, k8s performs a rolling restart, but on resource deletion,
# all pods are deleted concurrently due to the fact that we require
# PodManagementPolicy: appsv1.ParallelPodManagement for bootstrapping
# the cluster. So try to stop the nodes sequentially so that
# the last galera node stopped can set a "safe_to_bootstrap" flag.

if curl -s --cacert ${CACERT} --header "Content-Type:application/json" --header "Authorization: Bearer ${TOKEN}" -X GET ${APISERVER}/api/v1/namespaces/openstack/pods/${PODNAME} | grep -q '"code": *401'; then
    log "Galera resource is being deleted"
    nth=$(( ${PODNAME//*-/} + 1 ))
    while : ; do
        size=$(mysql -uroot -p"${DB_ROOT_PASSWORD}" -sNEe "show status like 'wsrep_cluster_size';" | tail -1)
        if [ ${size:-0} -gt $nth ]; then
            log "Waiting for cluster to scale down"
            sleep 2
        else
            break
        fi
    done
fi

# We now to need disconnect the clients so that when the server will
# initiate its shutdown, they won't receive unexpected WSREP statuses
# when running SQL queries.
# Note: It is safe to do it now, as k8s already removed this pod from
# the service endpoint, so client won't reconnect to it.

log "Close all active connections to this local galera node"
# filter out system and localhost connections, only consider clients with a port in the host field
# from that point, clients will automatically reconnect to another node
CLIENTS=$(mysql -uroot -p${DB_ROOT_PASSWORD} -nN -e "select id from information_schema.processlist where host like '%:%';")
echo -n "$CLIENTS" | tr '\n' ',' | xargs -r mysqladmin -uroot -p${DB_ROOT_PASSWORD} kill

# At this point no clients are connected anymore.
# We can finish this pre-stop hook and let k8s send the SIGTERM to the
# mysql server to make it disconnect from the galera cluster and shut down.
# Note: shutting down mysql here would cause the pod to finish too early,
# and this pre-stop hook would shows up as 'Failed' in k8s events.

exit 0
