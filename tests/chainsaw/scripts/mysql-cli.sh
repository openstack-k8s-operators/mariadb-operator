#!/bin/bash
set -u
ARGS=$1
SQL=$2
CMD="mysql -uroot -p\$DB_ROOT_PASSWORD $ARGS \"$SQL\""
oc exec -n ${NAMESPACE} -c galera openstack-galera-0 -- /bin/sh -c "$CMD"
