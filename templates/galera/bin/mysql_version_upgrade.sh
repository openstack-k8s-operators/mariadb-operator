#!/bin/bash
# pipefail is required so a failed `mariadb-upgrade` (which is piped to tee
# below) aborts this script with a non-zero exit. Kubernetes then keeps the
# pod out of the Available count, which is how the controller knows the
# upgrade has NOT yet succeeded on every replica.
set -euo pipefail

# Run mariadb-upgrade if the data files are from a different MariaDB major
# version than the current binary. This init container script runs before
# mysql_bootstrap.sh so the system tables are upgraded before the cluster forms.
#
# The operator keeps this init container in place for as long as
# spec.targetVersion is set, so the script runs on every pod start. The check
# is intentionally based on mysql_upgrade_info rather than comparing version
# strings from spec, so the script is idempotent: it exits immediately on a
# node that has already been upgraded or was freshly initialized.

# Extract the leading X.Y (major.minor) version from a MariaDB version string,
# tolerating any prefix (e.g. "mysqld  Ver 10.5.22-MariaDB for Linux ...")
# and any suffix (e.g. "-MariaDB", "-log", or distro packaging strings).
# Only major.minor is compared, so mariadb-upgrade runs on a major version
# upgrade (e.g. 10.5 -> 10.11) and not on a patch-level image update
# (e.g. 10.11.18 -> 10.11.20), matching spec.targetVersion's granularity.
version_num() {
    printf '%s\n' "$1" | grep -oP '\d+\.\d+' | head -1 || true
}

DATA_VERSION=$(version_num "$(cat /var/lib/mysql/mysql_upgrade_info 2>/dev/null || true)")
BINARY_VERSION=$(version_num "$(mysqld --version 2>/dev/null || true)")

if [[ -z "$DATA_VERSION" ]]; then
    echo "No existing data version (mysql_upgrade_info absent or unparseable), skipping upgrade"
    exit 0
fi

if [[ -z "$BINARY_VERSION" ]]; then
    echo "Could not determine mysqld binary version; aborting upgrade check" >&2
    exit 1
fi

if [[ "$DATA_VERSION" = "$BINARY_VERSION" ]]; then
    echo "Data already at $BINARY_VERSION, no upgrade needed"
    exit 0
fi

# The data files need upgrading. Only do that if the upgrade was requested,
# i.e. spec.targetVersion on the Galera CR is the version this image runs.
#
# A major version upgrade has to be requested by changing spec.targetVersion
# together with the image. Changing targetVersion makes the operator stop the
# whole cluster first, so that every node is upgraded while none is running,
# and the cluster never runs two MariaDB versions at once.
#
# If only the image changed, the StatefulSet does an ordinary rolling update
# instead: pods restart one at a time, and the others keep running the old
# version. Upgrading this node now would make it join a cluster of
# old-version nodes. So refuse to start instead. This pod stays down, the
# rolling update stops here, and the remaining nodes keep serving on the old
# image. Setting spec.targetVersion to match the image starts a proper upgrade.
#
# targetVersion is read from the API rather than passed in the pod spec, as a
# change to the pod spec would restart the pods by itself.
APISERVER=https://kubernetes.default.svc
SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount
GALERA_CR=$(curl -s \
    --cacert "${SERVICEACCOUNT}/ca.crt" \
    --header "Authorization: Bearer $(cat ${SERVICEACCOUNT}/token)" \
    "${APISERVER}/apis/mariadb.openstack.org/v1beta1/namespaces/$(cat ${SERVICEACCOUNT}/namespace)/galeras/{{.galeraInstanceName}}" || true)
# note jq is not installed in the galera image, using python instead
REQUESTED_VERSION=$(version_num "$(printf '%s' "${GALERA_CR}" | python3 -c "import json, sys; print(json.load(sys.stdin)['spec'].get('targetVersion', ''))" 2>/dev/null || true)")

if [[ -z "$REQUESTED_VERSION" ]]; then
    echo "Data files are at MariaDB $DATA_VERSION and this image runs $BINARY_VERSION, but spec.targetVersion could not be read from the Galera CR; not upgrading. Will retry." >&2
    exit 1
fi

if [[ "$REQUESTED_VERSION" != "$BINARY_VERSION" ]]; then
    echo "Refusing to upgrade data files from MariaDB $DATA_VERSION to $BINARY_VERSION: spec.targetVersion is $REQUESTED_VERSION." >&2
    echo "This image runs MariaDB $BINARY_VERSION, which was not requested. To upgrade, set spec.targetVersion to $BINARY_VERSION; the operator then stops the whole cluster and upgrades every node together. Otherwise, go back to an image that runs MariaDB $DATA_VERSION." >&2
    exit 1
fi

echo "Upgrading data files: $DATA_VERSION -> $BINARY_VERSION"

UPGRADE_PIDFILE=/var/tmp/upgrade.pid
MYSQLD_LOGFILE=/var/tmp/upgrade.log
UPGRADE_LOGFILE=/var/lib/mysql/mariadb_upgrade_${DATA_VERSION}_${BINARY_VERSION}.log

# This init container runs before mysql_bootstrap.sh, so /etc/my.cnf.d (the
# config-data-generated EmptyDir) is still empty and the server config the
# rest of the pod uses does not exist yet. Name datadir and socket explicitly
# rather than relying on the values compiled into the binary.
UPGRADE_DATADIR=/var/lib/mysql
UPGRADE_SOCKET=/var/lib/mysql/mysql.sock

rm -f "${UPGRADE_PIDFILE}" "${MYSQLD_LOGFILE}"

mysqld_safe --wsrep-on=OFF --skip-grant-tables \
    --datadir="${UPGRADE_DATADIR}" \
    --socket="${UPGRADE_SOCKET}" \
    --pid-file="${UPGRADE_PIDFILE}" \
    --log-error="${MYSQLD_LOGFILE}" &

# Wait for mysqld to be ready
TIMEOUT=${DB_MAX_TIMEOUT:-60}
while [[ ! -S "${UPGRADE_SOCKET}" ]] || \
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

mariadb-upgrade --socket="${UPGRADE_SOCKET}" 2>&1 | tee "${UPGRADE_LOGFILE}"

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
mysqladmin --socket="${UPGRADE_SOCKET}" -uroot -p"${DB_ROOT_PASSWORD}" shutdown

echo "shutdown complete"
