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
if [ -f "${MY_CNF}" ]; then
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
SECRET_NAME=$(echo "${GALERA_CR}" | python3 -c "import json, sys; print(json.load(sys.stdin)['spec']['secret'])")

# get password from secret
PASSWORD=$(curl -s \
    --cacert ${CACERT} \
    --header "Content-Type:application/json" \
    --header "Authorization: Bearer ${TOKEN}" \
    "${APISERVER}/${K8S_API}/namespaces/${NAMESPACE}/secrets/${SECRET_NAME}" \
    | python3 -c "import json, sys; print(json.load(sys.stdin)['data']['DatabasePassword'])" \
    | base64 -d)


# test again; warn if it doesn't work, however write to my.cnf in any
# case to allow the calling script to continue
if [ "${USE_SOCKET}" = "false" ] || [ -S "${MYSQL_SOCKET}" ]; then
    if ! mysql ${MYSQL_CONN_PARAMS} -uroot -p"${PASSWORD}" -e "SELECT 1;" >/dev/null 2>&1; then
        echo "WARNING: password retrieved from cluster failed authentication" >&2
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
