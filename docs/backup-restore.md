# Galera Backup and Restore

This document describes how to use the `GaleraBackup` and `GaleraRestore`
custom resources to back up and restore MariaDB Galera clusters managed
by the mariadb-operator.

The full CRD type definitions are available in the source:
[GaleraBackup](https://github.com/dciabrin/mariadb-operator/blob/master/api/v1beta1/galerabackup_types.go),
[GaleraRestore](https://github.com/dciabrin/mariadb-operator/blob/master/api/v1beta1/galerarestore_types.go).

## Overview

The backup and restore workflow relies on two custom resources:

- **GaleraBackup** sets up the infrastructure for recurring backups of a
  Galera cluster: a CronJob, one or two PersistentVolumeClaims, and the
  necessary RBAC resources.
- **GaleraRestore** creates a pod with access to the backup data so you
  can restore SQL dumps into a running Galera cluster.

The backup process uses `garbd` (Galera Arbitrator) to capture a
consistent snapshot from a cluster node via SST (State Snapshot
Transfer), then converts it into compressed SQL dumps stored on a
persistent volume. The restore process replays those SQL dumps into the
target cluster.

## Setting up a backup

The simplest GaleraBackup only requires the name of the Galera cluster.
Storage class, size, and schedule all inherit defaults from the
referenced Galera CR:

```yaml
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: openstack
spec:
  databaseInstance: openstack
```

Once applied, the operator creates a daily CronJob `backup-openstack`,
a PVC `mysql-backup-openstack`, and the associated RBAC resources. Wait
for the CR to be ready:

```bash
oc wait --for=condition=Ready galerabackup/openstack
```

### Using a dedicated transfer volume

By default, backups use a single PVC for both intermediate binary data
(the SST snapshot) and the final SQL dumps. If you rely on
volume-level backups (e.g. CSI snapshots) on top of GaleraBackup, only
the SQL dump files need to be captured in those snapshots. The binary
data transferred over SST is transient and does not need to be
snapshotted. In this case, adding `transferStorage` separates the two
into distinct PVCs, so volume-level snapshots of the backup PVC only
contain the SQL dumps:

```yaml
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: openstack
spec:
  databaseInstance: openstack
  transferStorage:
    storageRequest: 400M
```

This creates `mysql-backup-openstack` for the SQL dumps (size inherited
from Galera, mounted at `/backup/data`) and `mysql-transfer-openstack`
for the transient SST data (400M, mounted at `/backup/transfer`).

### Customizing the backup schedule

By default, the CronJob runs once a day (`@daily`). You can override
this with any standard cron expression via the `schedule` field:

```yaml
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: openstack
spec:
  databaseInstance: openstack
  schedule: "0 */6 * * *"   # every 6 hours
```

The value is passed directly to the Kubernetes CronJob `schedule` field,
so any expression accepted by Kubernetes is valid (e.g. `"@hourly"`,
`"30 2 * * 0"` for Sundays at 02:30).

### Automatic cleanup of old backups

Without a `retention` setting, backup files accumulate on the PVC
indefinitely. Setting `retention` to a Go duration causes the backup job
to delete files older than that duration at the end of each run:

```yaml
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: openstack
spec:
  databaseInstance: openstack
  retention: 72h   # delete backups older than 3 days
```

The retention check runs after every successful backup. Only backup
files for the same cluster are affected; other files on the PVC are
left untouched.

### Backing up multiple clusters

Each GaleraBackup targets a single Galera CR. To back up multiple
clusters, create one GaleraBackup per cluster:

```yaml
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: openstackbackup
spec:
  databaseInstance: openstack
---
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: cluster2backup
spec:
  databaseInstance: cluster2
```

Both CronJobs can run concurrently without interference.

### Triggering a backup manually

The CronJob runs on its configured schedule, but you can trigger an
immediate backup by creating a Job from the CronJob:

```bash
oc create job --from=cronjob/backup-openstack manual-backup
oc wait --for=condition=complete --timeout=160s job/manual-backup
```

#### Custom timestamp

By default, backup files are timestamped with the current UTC time. You
can override the timestamp by injecting the `BACKUP_TIMESTAMP`
environment variable:

```bash
oc create job --from=cronjob/backup-openstack custom-backup \
  --dry-run=client -o json \
  | jq '.spec.template.spec.containers[0].env += [{"name":"BACKUP_TIMESTAMP","value":"my-custom-label"}]' \
  | oc apply -f -
```

The resulting backup files will be named
`openstack_backup_my-custom-label.sql.gz` and
`openstack_backup-grants_my-custom-label.sql.gz`.

### Backup files produced

Each backup run produces two compressed SQL files in the backup PVC:

| File | Contents |
|------|----------|
| `openstack_backup_<timestamp>.sql.gz` | All InnoDB databases (excluding `mysql`) |
| `openstack_backup-grants_<timestamp>.sql.gz` | User grants (excluding `root`) |

### Important: PVC lifecycle

Backup PVCs are **not** owned by the GaleraBackup CR. Deleting the CR
does not delete the PVCs, so backup data is preserved. You must delete
backup PVCs manually when they are no longer needed:

```bash
oc delete pvc mysql-backup-openstack
# If using transfer storage:
oc delete pvc mysql-transfer-openstack
```

## Restoring from a backup

To restore data from a backup, create a GaleraRestore CR that references
an existing GaleraBackup:

```yaml
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraRestore
metadata:
  name: openstackrestore
spec:
  backupSource: openstackbackup
```

The operator creates a pod `restore-openstackrestore` with the backup
data PVC mounted (but not the transfer PVC, to avoid storage scheduling
conflicts). It also adds a finalizer to the referenced GaleraBackup CR
to prevent its accidental deletion while a restore is in progress.

Wait for the restore pod to be running:

```bash
oc wait --for=condition=Ready galerarestore/openstackrestore
oc wait --for=condition=Ready pod/restore-openstackrestore
```

### Running the restore script

Once the pod is running, execute the restore script to replay the SQL
dumps into the Galera cluster:

```bash
oc exec restore-openstackrestore -- \
  /var/lib/backup-scripts/restore_galera --yes /backup/data/*.gz
```

During the restore, the script temporarily disables external traffic to
the Galera cluster by clearing the service endpoint selector. Traffic is
restored automatically when the script completes (or if it exits
unexpectedly, via a trap handler).

Verify the restored data:

```bash
oc exec -c galera openstack-galera-0 -- mysql -nN -e 'show databases;'
```

Clean up when done. Deleting the GaleraRestore CR also removes the
finalizer from the GaleraBackup:

```bash
oc delete galerarestore/openstackrestore
```

### Selective restore: data only or grants only

The `restore_galera` script supports a `--content` flag to control which
backup files are applied:

```bash
# Restore everything (data + grants) -- this is the default
oc exec restore-openstackrestore -- \
  /var/lib/backup-scripts/restore_galera --yes /backup/data/*.gz

# Restore only database data, skip grants
oc exec restore-openstackrestore -- \
  /var/lib/backup-scripts/restore_galera --yes --content data /backup/data/*.gz

# Restore only user grants
oc exec restore-openstackrestore -- \
  /var/lib/backup-scripts/restore_galera --yes --content grants /backup/data/*.gz
```

The script distinguishes data files from grants files by the `-grants`
substring in the filename.

Other options:

| Option | Description |
|--------|-------------|
| `-y`, `--yes` | Skip the confirmation prompt |
| `-d`, `--database DB` | Override the Galera CR name (defaults to `$DB` env var) |
| `-c`, `--content MODE` | What to restore: `all` (default), `data`, or `grants` |

### Cleaning up old backups before restoring

If multiple backups have accumulated on the PVC and you only want to
restore the most recent one, you can remove older files from the restore
pod before running the script:

```bash
# Get the timestamp of the backup job you want to restore from
BACKUPTIME=$(oc get -o json job manual-backup \
  | jq -r '.status.startTime | fromdate | tostring')

# Remove backup files older than that timestamp
oc exec restore-openstackrestore -- \
  find /backup/data -type f -not -newermt "@${BACKUPTIME}" -delete
```

## Full backup and restore walkthrough

This example demonstrates a complete backup and restore cycle for a
Galera cluster named `openstack`.

```bash
# 1. Create the GaleraBackup CR
cat <<EOF | oc apply -f -
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraBackup
metadata:
  name: openstackbackup
spec:
  databaseInstance: openstack
EOF

# 2. Wait for the backup infrastructure to be ready
oc wait --for=condition=Ready galerabackup/openstackbackup

# 3. Trigger a backup
oc create job --from=cronjob/backup-openstackbackup manual-backup
oc wait --for=condition=complete --timeout=160s job/manual-backup

# 4. (Later) Create the GaleraRestore CR
cat <<EOF | oc apply -f -
apiVersion: mariadb.openstack.org/v1beta1
kind: GaleraRestore
metadata:
  name: openstackrestore
spec:
  backupSource: openstackbackup
EOF

# 5. Wait for the restore pod
oc wait --for=condition=Ready galerarestore/openstackrestore
oc wait --for=condition=Ready pod/restore-openstackrestore

# 6. Run the restore
oc exec restore-openstackrestore -- \
  /var/lib/backup-scripts/restore_galera --yes /backup/data/*.gz

# 7. Verify
oc exec -c galera openstack-galera-0 -- mysql -nN -e 'show databases;'

# 8. Clean up
oc delete galerarestore/openstackrestore
```

## How it works internally

### Backup

1. The backup pod connects to the Galera cluster and retrieves the list
   of cluster members.
2. It selects a backup target node, preferring a node that is not the
   current service endpoint.
3. It uses `garbd` with rsync SST to capture a consistent binary
   snapshot from the target node into the transfer directory.
4. It starts a local MariaDB server on the snapshot data.
5. It runs `mysqldump` to produce compressed SQL dumps (data + grants).
6. The SQL dumps are written to the backup data directory with a `.tmp`
   suffix, then atomically renamed on success.

### Restore

1. The restore script connects to the Galera cluster via the service
   endpoint and identifies the target pod.
2. It temporarily disables external traffic to the cluster by clearing
   the service endpoint selector.
3. It replays each SQL dump file directly into the target pod.
4. It restores the service endpoint selector to re-enable traffic.
5. If the script exits unexpectedly, a trap handler restores the
   endpoint.
