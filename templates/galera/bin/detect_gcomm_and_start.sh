#!/bin/bash

set -eu

# OSPRH-27031: Conditional sourcing for backwards compatibility with old pods
# where script is updated but mysql_root_auth.sh is not yet available
if [ -f /var/lib/operator-scripts/mysql_root_auth.sh ]; then
    # When this container starts, retrieve up-to-date DB credentials
    # before starting the local mysql server:
    #   - disable password check, as when this script runs,
    #     the mysql server has not started yet.
    #   - discard warning logged while fetching new credentials
    MYSQL_ROOT_AUTH_BYPASS_CHECKS=true source /var/lib/operator-scripts/mysql_root_auth.sh 2>/dev/null
else
    # Old pod restart scenario: script updated but mysql_root_auth.sh not available
    if [ -z "${DB_ROOT_PASSWORD}" ]; then
        echo "WARNING: mysql_root_auth.sh not found and DB_ROOT_PASSWORD not set" >&2
    fi
    export MYSQL_PWD="${DB_ROOT_PASSWORD}"
fi

URI_FILE=/var/lib/mysql/gcomm_uri

rm -f /var/lib/mysql/mysql.sock
rm -f $URI_FILE

# Discover the state of the local galera database and report it
# back to the galera CR so the mariadb-operator can decide how
# to start mysqld in this container.
if [ -f /var/lib/operator-scripts/report_local_galera_state.py ]; then
    LOGFILE=$(my_print_defaults mysqld | grep log-error | cut -d= -f2)
    if [ -f "${LOGFILE}" ]; then
        REPORT_ARGS="--file ${LOGFILE}"
    else
        REPORT_ARGS=""
    fi
    python3 /var/lib/operator-scripts/report_local_galera_state.py $REPORT_ARGS --push
fi

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
