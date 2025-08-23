#!/bin/bash
set -u
ARGS=$1
SQL=$2
ROOT_PW_AUTH="source /var/lib/operator-scripts/root_auth.sh; "
CMD="mysql -uroot -p\$DB_ROOT_PASSWORD $ARGS \"$SQL\""

oc exec -n ${NAMESPACE} -c galera openstack-galera-0 -- /bin/sh -c "${ROOT_PW_AUTH} ${CMD}"
