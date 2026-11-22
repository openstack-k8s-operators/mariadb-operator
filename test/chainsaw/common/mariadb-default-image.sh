#!/bin/bash
# Print the MariaDB container image the operator deploys by default.
#
# A Galera CR is created with --dry-run=server, so the operator's defaulting
# webhook fills in spec.containerImage without anything being deployed. This
# works whether the operator runs in a pod or locally, as long as its webhooks
# are installed. For an operator running without webhooks, export
# RELATED_IMAGE_MARIADB to the image it would deploy.
set -eu

if [ -n "${RELATED_IMAGE_MARIADB:-}" ]; then
    printf '%s' "${RELATED_IMAGE_MARIADB}"
    exit 0
fi

# spec.containerImage is a required field that the webhook defaults, so
# without a webhook this create fails rather than returning an empty image.
if ! IMAGE=$(oc create --dry-run=server -n "${NAMESPACE}" -o jsonpath='{.spec.containerImage}' -f - <<EOF
apiVersion: mariadb.openstack.org/v1beta1
kind: Galera
metadata:
  name: mariadb-default-image-probe
spec:
  replicas: 1
  secret: osp-secret
  storageClass: local-storage
  storageRequest: 500M
EOF
) || [ -z "${IMAGE}" ]; then
    echo "could not determine the default mariadb image: the operator's defaulting webhook is not answering. Set RELATED_IMAGE_MARIADB to run against an operator without webhooks." >&2
    exit 1
fi

printf '%s' "${IMAGE}"
