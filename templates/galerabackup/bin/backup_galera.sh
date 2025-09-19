#!/bin/bash

set -eu

# API server config
APISERVER=https://kubernetes.default.svc
SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount
NAMESPACE=$(cat ${SERVICEACCOUNT}/namespace)
TOKEN=$(cat ${SERVICEACCOUNT}/token)
CACERT=${SERVICEACCOUNT}/ca.crt

# Backup configuration
JOINER_LOG=/tmp/joiner.log
TRANSFER_DIR=/backup/transfer
BACKUP_DIR=/backup/data

# Only rsync SST is supported for the time being
WSREP_SST=wsrep_sst_rsync

function log() {
    echo "$(date +%F_%H_%M_%S) `basename $0` $*"
}

function err() {
    echo "$(date +%F_%H_%M_%S) `basename $0` ERROR: $*"
    exit 1
}

function check_arg() {
    local arg=$1
    local desc=$2
    if ! env | cut -d= -f1 | grep -q -e "^${arg}$"; then
        err "Missing env parameter $arg ($desc)"
    fi
}

function finally {
    last_status=$?
    timefmt=$(date -d "@$SECONDS" -u +'%H hours %M minutes %S seconds' | sed -e 's/00 [^ ]* //g' -e 's/0\([0-9]\)/\1/g')
    log "Backup of Galera cluster ${DB} finished in ${timefmt} (ret: ${last_status})"
    # do not leave incomplete dumps behind
    rm -f ${BACKUP_DIR}/${DB}_backup*.tmp
}

# Environment variables
check_arg DB "name of the Galera CR to backup"
check_arg SECRET "name of the k8s secret that holds the backup user credentials"
check_arg RETENTION "time to keep the old backup files on the filesystem"


timestamp=$(date -u +'%Y-%m-%d_%H-%M-%S')
log "Starting backup of Galera cluster ${DB} - ${timestamp}"
trap finally EXIT


#
# SETUP
#  - configure backup actions based on cluster specs
#  - prepare local storage prior to backup
#
if [ "${TLS:-}" = "true" ]; then
    MYSQL_CFG=/etc/mysql_backup_tls.cnf
    WSREP_SST_CFG=/etc/mysql_backup_tls.cnf
    . /etc/garbd_backup_tls.cnf
    GARBD_OPTS=";${GARBD_TLS_OPTS}"
fi

# prepare backup destination
for dir in ${TRANSFER_DIR} ${BACKUP_DIR}; do
    if [ ! -d $dir ]; then mkdir -p $dir; fi
done

# clean up potential leftover state from previous backups
find ${TRANSFER_DIR} -maxdepth 1 -name '*sst.pid' -delete


#
# SELECT TARGET GALERA NODE TO TAKE BACKUP FROM
#  - when possible choose a node that is not the current endpoint
#

log "Retrieve user credentials for Galera cluster ${DB}"
output=$(curl -s --cacert ${CACERT} --header "Content-Type:application/json" --header "Authorization: Bearer ${TOKEN}" --request GET ${APISERVER}/api/v1/namespaces/${NAMESPACE}/secrets/${SECRET})
dbpass=$(echo "${output}" | python3 -c 'import base64,json,sys;s=json.load(sys.stdin);print(base64.b64decode(s["data"]["DbRootPassword"]).decode())')
dbuser=root

log "Retrieve the active endpoint for Galera cluster ${DB}"
service=${DB}
output=$(curl -s --cacert ${CACERT} --header "Content-Type:application/json" --header "Authorization: Bearer ${TOKEN}" --request GET ${APISERVER}/api/v1/namespaces/${NAMESPACE}/services/${service})
endpoint=$(echo "${output}" | python3 -c 'import json,sys;s=json.load(sys.stdin);print(s["spec"]["selector"]["statefulset.kubernetes.io/pod-name"])')

log "Retrieve available members (from the quorated partition)"
endpointfqdn=${service}.${NAMESPACE}.svc
members=$(mysql --defaults-file="${MYSQL_CFG:-}" -nN -u${dbuser} -p${dbpass} -h ${endpointfqdn} -e 'select node_name from mysql.wsrep_cluster_members;')
log "Members found: $(echo ${members}) (service endpoint: $endpoint)"

size=$(echo -n "${members}" | wc -w)
if [ $size -eq 0 ]; then
    target=""
elif [ $size -eq 1 ]; then
    target=$endpoint
else
    target=$(echo "$members" | grep -v "$endpoint" | head -1)
fi

if [ "$target" = "" ]; then
    err "Could not select a target to backup Galera cluster ${DB}"
else
    log "Selecting ${target} as node to take backup from"
fi


#
# TRANSFER SNAPSHOT FROM THE TARGET GALERA NODE
#  - use gard to request a consistent snapshot via SST
#  - rely on wsrep_sst_rsync to receive snapshot and handle errors and cleanup
#

# use FQDN to validate TLS connections
joinerfqdn=$(hostname -s).${DB}-galera.${NAMESPACE}.svc
targetfqdn=${target}.${DB}-galera.${NAMESPACE}.svc

log "Start the joiner SST script"
touch ${JOINER_LOG}
tail -F ${JOINER_LOG} &
tailpid=$!

$WSREP_SST --defaults-file "${WSREP_SST_CFG:-}"  --defaults-group-suffix '' --role 'joiner' --address "${joinerfqdn}:4444" --datadir ${TRANSFER_DIR} --parent $$ >${JOINER_LOG} 2>&1 &
joinerpid=$!

while kill -s 0 $joinerpid 2>/dev/null && ! grep -q '^ready ' $JOINER_LOG; do
    log "Wait for the joiner SST script to be ready"
    sleep 1
done
if ! kill -s 0 $joinerpid 2>/dev/null; then
    wait $joinerpid
    res=$?
    err "joiner SST script returned error ${res}, backup of Galera cluster ${DB} failed"
fi
kill $tailpid 2>/dev/null

log "Capture a Galera backup with garbd over SST"
garbd -o "pc.weight=2${GARBD_OPTS:-}" -n backup -a gcomm://${targetfqdn}:4567 -g galera_cluster -l /dev/stdout --sst rsync:${joinerfqdn}:4444/rsync_sst  --donor ${target}
res=$?
if [ $res -ne 0 ]; then
    err "garbd returned error ${res}, backup of Galera cluster ${DB} failed"
else
    log "garbd finished succesfully"
fi

while kill -s 0 $joinerpid 2>/dev/null; do
    log "Wait for the joiner SST script to exit"
    sleep 1
done


#
# CONVERT THE SNAPSHOT INTO SQL BACKUP FILES
#  - perform a standard mysqldump of the local snapshot DB
#  - run additional steps for backup housekeeping
#

log "Prepare logical backup from the captured Galera database"

log "Start a local mariadb server"
/usr/libexec/mysqld --no-defaults --datadir=${TRANSFER_DIR} --skip-networking --skip-log-error --pid-file=/tmp/mariadb.pid --socket=/tmp/mariadb.sock &

while [ ! -e /tmp/mariadb.sock ]; do
    log "Wait for the mariadb server to be ready"
    sleep 1
done

# Mark backup files as temporary until the dumps finish succesfully
log "Preparing a logical backup of the database"
mysql -u${dbuser} -p${dbpass} --socket=/tmp/mariadb.sock -s -N -e "select distinct table_schema from information_schema.tables where engine='innodb' and table_schema != 'mysql';" | xargs -r mysqldump -u${dbuser} -p${dbpass} --socket=/tmp/mariadb.sock --single-transaction --databases | gzip -c > ${BACKUP_DIR}/${DB}_backup_${timestamp}.sql.gz.tmp

log "Preparing a logical backup of the database's users and grants"
mysql -u${dbuser} -p${dbpass} --socket=/tmp/mariadb.sock -s -N -e "SELECT CONCAT('\"SHOW GRANTS FOR ''',user,'''@''',host,''';\"') FROM mysql.user where (length(user) > 0 and user NOT LIKE 'root')" | xargs -n1 mysql -u${dbuser} -p${dbpass} --socket=/tmp/mariadb.sock -s -N -e | sed 's/$/;/' | gzip -c > ${BACKUP_DIR}/${DB}_backup-grants_${timestamp}.sql.gz.tmp

log "Stop the local mariadb server"
mysqladmin -u${dbuser} -p${dbpass} --socket=/tmp/mariadb.sock shutdown

# Lift the temporary mark for new backup files
mv -f ${BACKUP_DIR}/${DB}_backup_${timestamp}.sql.gz{.tmp,}
mv -f ${BACKUP_DIR}/${DB}_backup-grants_${timestamp}.sql.gz{.tmp,}
log "New backup of Galera cluster ${DB} created successful"


#
# ADDITIONAL/OPTIONAL STEPS
#

if [ -n "${RETENTION}" ]; then
    log "Removing previous backups of Galera cluster ${DB} which are older than '$RETENTION' minutes"
    find "${BACKUP_DIR}" -name "${DB}*.sql" -mmin "+${RETENTION}" -print -prune
fi
