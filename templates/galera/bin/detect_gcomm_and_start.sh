#!/bin/bash

set -eu

# OSPRH-27031: Conditional sourcing for backwards compatibility with old pods
# where script is updated but mysql_root_auth.sh is not yet available
if [ -f /var/lib/operator-scripts/mysql_root_auth.sh ]; then
    source /var/lib/operator-scripts/mysql_root_auth.sh
else
    # Old pod restart scenario: script updated but mysql_root_auth.sh not available
    if [ -z "${DB_ROOT_PASSWORD}" ]; then
        echo "WARNING: mysql_root_auth.sh not found and DB_ROOT_PASSWORD not set" >&2
    fi
fi

URI_FILE=/var/lib/mysql/gcomm_uri

rm -f /var/lib/mysql/mysql.sock
rm -f $URI_FILE

echo "Waiting for gcomm URI to be configured for this POD"
while [ ! -f $URI_FILE ]; do
    sleep 2
done

set -x
URI=$(cat $URI_FILE)
if [ "$URI" = "gcomm://" ]; then
    echo "this POD will now bootstrap a new galera cluster"
    if [ -f /var/lib/mysql/grastate.dat ]; then
        sed -i -e 's/^\(safe_to_bootstrap\):.*/\1: 1/' /var/lib/mysql/grastate.dat
    fi
else
    echo "this POD will now join cluster $URI"
fi

rm -f $URI_FILE
exec /usr/libexec/mysqld --wsrep-cluster-address="$URI"
