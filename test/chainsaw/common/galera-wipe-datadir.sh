#!/bin/bash
# Empty the MariaDB datadir on a Galera PVC, so that a test which leaves
# deliberately broken data files behind still starts from a known state on its
# next run. Does nothing if the PVC does not exist yet.
#
# The PVC name is the only argument, e.g. mysql-db-upgrade-refuse-galera-0.
# A StatefulSet does not delete the volumes it claimed, so a PVC outlives the
# Galera CR and whatever the last run wrote to it is still there.
set -eu

PVC=$1
POD=galera-wipe-datadir

if ! oc get pvc -n "${NAMESPACE}" "${PVC}" >/dev/null 2>&1; then
    echo "${PVC} does not exist yet, nothing to wipe"
    exit 0
fi

IMAGE=$("$(dirname "$0")/mariadb-default-image.sh")

oc delete pod -n "${NAMESPACE}" "${POD}" --ignore-not-found

# The galera pods mount the PVC at /var/lib/mysql under its mysql subPath and
# run as the mysql user (users.MysqlUID/MysqlGID in lib-common, which is what
# owns the datadir); mount it the same way so the datadir can be emptied.
oc apply -n "${NAMESPACE}" -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${POD}
spec:
  restartPolicy: Never
  securityContext:
    runAsUser: 42434
    runAsGroup: 42434
    fsGroup: 42434
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: ${POD}
    image: ${IMAGE}
    command: ["bash", "-c", "shopt -s dotglob nullglob; rm -rf /var/lib/mysql/*; echo 'datadir now holds:'; ls -A /var/lib/mysql"]
    volumeMounts:
    - name: mysql-db
      mountPath: /var/lib/mysql
      subPath: mysql
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop: ["ALL"]
  volumes:
  - name: mysql-db
    persistentVolumeClaim:
      claimName: ${PVC}
EOF

if ! oc wait pod -n "${NAMESPACE}" "${POD}" --for=jsonpath='{.status.phase}'=Succeeded --timeout=300s; then
    oc describe pod -n "${NAMESPACE}" "${POD}"
    exit 1
fi

oc logs -n "${NAMESPACE}" "${POD}"
oc delete pod -n "${NAMESPACE}" "${POD}" --ignore-not-found
