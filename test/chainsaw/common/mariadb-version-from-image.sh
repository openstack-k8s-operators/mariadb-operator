#!/bin/bash
# Print the MariaDB major.minor version (e.g. 10.11) that a container image
# runs. The image is the first argument and defaults to the one the operator
# deploys. Nothing in the cluster knows the version an image provides, so the
# image is started once with `mysqld --version`.
#
# Only the version goes to stdout, so a chainsaw script step can capture it in
# a binding; everything else goes to stderr.
set -eu

IMAGE=${1:-$("$(dirname "$0")/mariadb-default-image.sh")}
POD=mariadb-version-probe

oc delete pod -n "${NAMESPACE}" "${POD}" --ignore-not-found >&2

oc apply -n "${NAMESPACE}" -f - >&2 <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${POD}
spec:
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: ${POD}
    image: ${IMAGE}
    command: ["mysqld", "--version"]
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop: ["ALL"]
EOF

if ! oc wait pod -n "${NAMESPACE}" "${POD}" --for=jsonpath='{.status.phase}'=Succeeded --timeout=300s >&2; then
    oc describe pod -n "${NAMESPACE}" "${POD}" >&2
    exit 1
fi

RAW=$(oc logs -n "${NAMESPACE}" "${POD}")
oc delete pod -n "${NAMESPACE}" "${POD}" --ignore-not-found >&2

VERSION=$(printf '%s\n' "${RAW}" | grep -oE '[0-9]+\.[0-9]+' | head -n1 || true)
if [ -z "${VERSION}" ]; then
    echo "could not parse a MariaDB version from: ${RAW}" >&2
    exit 1
fi

echo "image ${IMAGE} runs MariaDB ${VERSION}" >&2
printf '%s' "${VERSION}"
