#!/bin/bash
set +eux

# Get pod name
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

# note jq is not installed in the galera image, macgyvering w/ python instead

SECRET_NAME=$(curl -s \
    --cacert ${CACERT} \
    --header "Content-Type:application/json" \
    --header "Authorization: Bearer ${TOKEN}" \
    "${APISERVER}/${MARIADB_API}/namespaces/${NAMESPACE}/galeras/${GALERA_INSTANCE}" \
    | python3 -c "import json, sys; print(json.load(sys.stdin)['spec']['secret'])")

PASSWORD=$(curl -s \
    --cacert ${CACERT} \
    --header "Content-Type:application/json" \
    --header "Authorization: Bearer ${TOKEN}" \
    "${APISERVER}/${K8S_API}/namespaces/${NAMESPACE}/secrets/${SECRET_NAME}" \
    | python3 -c "import json, sys; print(json.load(sys.stdin)['data']['DbRootPassword'])" \
    | base64 -d)

MYSQL_PWD="${PASSWORD}"
DB_ROOT_PASSWORD="${PASSWORD}"

export MYSQL_PWD
export DB_ROOT_PASSWORD
