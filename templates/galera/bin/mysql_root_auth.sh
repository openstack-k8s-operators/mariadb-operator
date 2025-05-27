#!/bin/bash
set +eu

POD_NAME=$(hostname)

# API server config
APISERVER=https://kubernetes.default.svc
SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount
NAMESPACE=$(cat ${SERVICEACCOUNT}/namespace)
TOKEN=$(cat ${SERVICEACCOUNT}/token)
CACERT=${SERVICEACCOUNT}/ca.crt
K8S_API="api/v1"
MARIADB_API="apis/mariadb.openstack.org/v1beta1"

GALERA_INSTANCE="{{.galeraInstanceName}}"

MY_CNF="$HOME/.my.cnf"
MYSQL_SOCKET=/var/lib/mysql/mysql.sock

CREDENTIALS_CHECK_TIMEOUT=4

# Set up connection parameters based on whether we're connecting remotely or locally
if [ -n "${MYSQL_REMOTE_HOST}" ]; then

    MYSQL_CONN_PARAMS="-h ${MYSQL_REMOTE_HOST} -P 3306"
    USE_SOCKET=false
else
    MYSQL_CONN_PARAMS=""
    USE_SOCKET=true
fi

# Check if we have cached credentials
if [ "${MYSQL_ROOT_AUTH_BYPASS_CHECKS}" != "true" ] && [ -f "${MY_CNF}" ]; then
    # Read the password from .my.cnf
    PASSWORD=$(grep '^password=' "${MY_CNF}" | cut -d= -f2-)

    # Validate credentials if MySQL is accessible
    if [ -n "${PASSWORD}" ]; then
        # For local connections, check if socket exists; for remote, always try
        SHOULD_VALIDATE=false
        if [ "${USE_SOCKET}" = "true" ]; then
            if [ -S "${MYSQL_SOCKET}" ]; then
                SHOULD_VALIDATE=true
            fi
        else
            # Remote connection - always validate
            SHOULD_VALIDATE=true
        fi

        credentials_check=1
        if [ "${SHOULD_VALIDATE}" = "true" ]; then
            timeout ${CREDENTIALS_CHECK_TIMEOUT} mysql ${MYSQL_CONN_PARAMS} -uroot -p"${PASSWORD}" -e "SELECT 1;" >/dev/null 2>&1
            credentials_check=$?
        fi

        if [ "${SHOULD_VALIDATE}" = "true" ] && [ $credentials_check -eq 124 ]; then
            # MySQL validation timed out, assume cache is valid and will be validated on next probe
            export MYSQL_PWD="${PASSWORD}"
            export DB_ROOT_PASSWORD="${PASSWORD}"
            return 0
        elif [ "${SHOULD_VALIDATE}" = "true" ] && [ $credentials_check -eq 0 ]; then
            # Credentials are still valid, use cached values
            export MYSQL_PWD="${PASSWORD}"
            export DB_ROOT_PASSWORD="${PASSWORD}"
            return 0
        elif [ "${USE_SOCKET}" = "true" ] && [ ! -S "${MYSQL_SOCKET}" ]; then
            # MySQL not running locally, assume cache is valid and will be validated on next probe
            export MYSQL_PWD="${PASSWORD}"
            export DB_ROOT_PASSWORD="${PASSWORD}"
            return 0
        fi
    fi
    # If we get here, credentials are invalid, fall through to refresh

fi


# Get the Galera CR
GALERA_CR=$(curl -s \
    --cacert ${CACERT} \
    --header "Content-Type:application/json" \
    --header "Authorization: Bearer ${TOKEN}" \
    "${APISERVER}/${MARIADB_API}/namespaces/${NAMESPACE}/galeras/${GALERA_INSTANCE}")

# note jq is not installed in the galera image, macgyvering w/ python instead
SECRET_NAME=$(echo "${GALERA_CR}" | python3 -c "import json, sys; print(json.load(sys.stdin)['status']['rootDatabaseSecret'])")

# get password from secret
PASSWORD=$(curl -s \
    --cacert ${CACERT} \
    --header "Content-Type:application/json" \
    --header "Authorization: Bearer ${TOKEN}" \
    "${APISERVER}/${K8S_API}/namespaces/${NAMESPACE}/secrets/${SECRET_NAME}" \
    | python3 -c "import json, sys; print(json.load(sys.stdin)['data']['DatabasePassword'])" \
    | base64 -d)

# Special step for the unlikely case that root PW is being changed but the
# account.sh script failed to complete.  Test this password (which came from
# galera->Status->rootDatabaseSecret) and if not working, see if there is a
# different (newer) password in root galera->Spec->rootDatabaseAccount->Secret,
# and try that. This suits the case where a new password was placed in
# galera->Spec->rootDatabaseAccount->Secret, account.sh ran to update the root
# password, but failed to complete, even though the actual password got
# updated.   account.sh will run again on a new pod but the password that's in
# galera->Status->rootDatabaseSecret is no longer valid, and would prevent
# account.sh from proceeding a second time.  Try the "pending" password just to
# get through, so that account.sh can succeed and
# galera->Status->rootDatabaseSecret can then be updated.

PASSWORD_VALID=true

# test password with mysql command if socket exists, or we are remote
if [ "${MYSQL_ROOT_AUTH_BYPASS_CHECKS}" != "true" ] && { [ "${USE_SOCKET}" = "false" ] || [ -S "${MYSQL_SOCKET}" ]; }; then
    if ! mysql ${MYSQL_CONN_PARAMS} -uroot -p"${PASSWORD}" -e "SELECT 1;" >/dev/null 2>&1; then
        echo "WARNING: primary password retrieved from cluster failed authentication; will try fallback password" >&2
        PASSWORD_VALID=false
    fi
fi

# if password failed, look for alternate password from the mariadbdatabaseaccount
# spec directly.  assume we are in root pw flight
if [ "${PASSWORD_VALID}" = "false" ]; then

    MARIADB_ACCOUNT=$(echo "${GALERA_CR}" | python3 -c "import json, sys; print(json.load(sys.stdin)['spec']['rootDatabaseAccount'] or '${GALERA_INSTANCE}-mariadb-root')")

    MARIADB_ACCOUNT_CR=$(curl -s \
        --cacert ${CACERT} \
        --header "Content-Type:application/json" \
        --header "Authorization: Bearer ${TOKEN}" \
        "${APISERVER}/${MARIADB_API}/namespaces/${NAMESPACE}/mariadbaccounts/${MARIADB_ACCOUNT}")

    # look in spec.secret
    FALLBACK_SECRET_NAME=$(echo "${MARIADB_ACCOUNT_CR}" | python3 -c "import json, sys; print(json.load(sys.stdin)['spec']['secret'])")

    # Get the new password from the fallback secret
    PASSWORD=$(curl -s \
        --cacert ${CACERT} \
        --header "Content-Type:application/json" \
        --header "Authorization: Bearer ${TOKEN}" \
        "${APISERVER}/${K8S_API}/namespaces/${NAMESPACE}/secrets/${FALLBACK_SECRET_NAME}" \
        | python3 -c "import json, sys; print(json.load(sys.stdin)['data']['DatabasePassword'])" \
        | base64 -d)

    # test again; warn if it doesn't work, however write to my.cnf in any
    # case to allow the calling script to continue
    if ! mysql ${MYSQL_CONN_PARAMS} -uroot -p"${PASSWORD}" -e "SELECT 1;" >/dev/null 2>&1; then
        echo "WARNING: Both primary and fallback passwords failed authentication, will maintain fallback password" >&2
    fi

fi


MYSQL_PWD="${PASSWORD}"
DB_ROOT_PASSWORD="${PASSWORD}"

# Cache credentials to /root/.my.cnf in MySQL client format
cat > "${MY_CNF}" <<EOF
[client]
user=root
password=${PASSWORD}
EOF

# Set restrictive permissions on .my.cnf
chmod 600 "${MY_CNF}"

export MYSQL_PWD
export DB_ROOT_PASSWORD
