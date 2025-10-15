#!/bin/bash

set -eu

source /var/lib/operator-scripts/mysql_root_auth.sh

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
