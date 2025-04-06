#!/bin/bash
set +eux

# Get pod name and namespace
POD_NAME=$(hostname)
NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)

K8S_API="api/v1"
MARIADB_API="apis/mariadb.openstack.org/v1beta1"

GALERA_INSTANCE="{{.galeraInstanceName}}"

# note jq is not installed in the galera image, macgyvering w/ python instead

SECRET_NAME=$(curl -s \
    --cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt \
    -H "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" \
    "https://kubernetes.default.svc/$MARIADB_API/namespaces/$NAMESPACE/galeras/$GALERA_INSTANCE" \
    | python3 -c "import json, sys; print(json.load(sys.stdin)['spec']['secret'])")

PASSWORD=$(curl -s \
    --cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt \
    -H "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" \
    "https://kubernetes.default.svc/$K8S_API/namespaces/$NAMESPACE/secrets/${SECRET_NAME}" \
    | python3 -c "import json, sys; print(json.load(sys.stdin)['data']['DbRootPassword'])" \
    | base64 -d)

MYSQL_PWD="${PASSWORD}"
DB_ROOT_PASSWORD="${PASSWORD}"

export MYSQL_PWD
export DB_ROOT_PASSWORD
