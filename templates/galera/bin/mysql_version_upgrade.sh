#!/bin/bash
set -eu

# Run mariadb-upgrade if the data files are from a different MariaDB version
# than the current binary. This init container script runs before
# mysql_bootstrap.sh so the system tables are upgraded before the cluster forms.
#
# The check is intentionally based on mysql_upgrade_info rather than comparing
# version strings from spec, so the script is idempotent: it exits immediately
# on a node that has already been upgraded or was freshly initialized.

DATA_VERSION=$(cat /var/lib/mysql/mysql_upgrade_info 2>/dev/null || echo "")
BINARY_VERSION=$(mysqld --version | grep -oP '\d+\.\d+\.\d+-MariaDB' || echo "")

if [[ -z "$DATA_VERSION" ]]; then
    echo "No existing data (mysql_upgrade_info absent), skipping upgrade"
    exit 0
fi

if [[ "$DATA_VERSION" = "$BINARY_VERSION" ]]; then
    echo "Data already at $BINARY_VERSION, no upgrade needed"
    exit 0
fi

echo "Upgrading data files: $DATA_VERSION -> $BINARY_VERSION"

DATA_VERSION_NUM=${DATA_VERSION%%[!0-9.]*}
BINARY_VERSION_NUM=${BINARY_VERSION%%[!0-9.]*}

UPGRADE_PIDFILE=/var/tmp/upgrade.pid
MYSQLD_LOGFILE=/var/tmp/upgrade.log
UPGRADE_LOGFILE=/var/lib/mysql/mariadb_upgrade_${DATA_VERSION_NUM}_${BINARY_VERSION_NUM}.log

rm -f "${UPGRADE_PIDFILE}" "${MYSQLD_LOGFILE}"

mysqld_safe --wsrep-on=OFF --skip-grant-tables \
    --pid-file="${UPGRADE_PIDFILE}" \
    --log-error="${MYSQLD_LOGFILE}" &

# Wait for mysqld to be ready
TIMEOUT=${DB_MAX_TIMEOUT:-60}
while [[ ! -S /var/lib/mysql/mysql.sock ]] || \
      [[ ! -f "${UPGRADE_PIDFILE}" ]]; do
    if [[ ${TIMEOUT} -gt 0 ]]; then
        TIMEOUT=$((TIMEOUT - 1))
        sleep 1
    else
        echo "Timed out waiting for mysqld to start for upgrade"
        cat "${MYSQLD_LOGFILE}"
        exit 1
    fi
done

mariadb-upgrade 2>&1 | tee "${UPGRADE_LOGFILE}"

# do a graceful shutdown of mysqld using mysqladmin.  note that if all of the
# below fails, the container exits anyway.
echo "Upgrade complete.  Will shut down mysql now"

# mariadb-upgrade runs FLUSH PRIVILEGES which re-enables the grant tables,
# disabling --skip-grant-tables. Fetch the root password from the K8s API
# so mysqladmin can authenticate for shutdown.

# only use the k8s API to get the current root password; root password
# cache will not be set up yet
MYSQL_ROOT_AUTH_BYPASS_CHECKS=true

# get the root password
source /var/lib/operator-scripts/mysql_root_auth.sh

# do the shutdown
mysqladmin -uroot -p"${DB_ROOT_PASSWORD}" shutdown

echo "shutdown complete"
