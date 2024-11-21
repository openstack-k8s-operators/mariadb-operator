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

log "Initiating orchestrated shutdown of the local galera node"

log "Failover service to another available galera node"
bash $(dirname $0)/mysql_wsrep_notify.sh --status failover

log "Close all active connections to this local galera node"
# filter out system and localhost connections, only consider clients with a port in the host field
# from that point, clients will automatically reconnect to another node
CLIENTS=$(mysql -uroot -p${DB_ROOT_PASSWORD} -nN -e "select id from information_schema.processlist where host like '%:%';")
echo -n "$CLIENTS" | tr '\n' ',' | xargs mysqladmin -uroot -p${DB_ROOT_PASSWORD} kill

log "Shutdown local server"
mysqladmin -uroot -p"${DB_ROOT_PASSWORD}" shutdown
