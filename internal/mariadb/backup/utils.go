package mariadbbackup

import (
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

// BackupCronJobName - name of the cronjob resource created for a backup CR
func BackupCronJobName(b *mariadbv1.GaleraBackup) string {
	return "backup-" + b.Name
}

// BackupPVCName returns the name of the PVC resource that stores .sql backup files
func BackupPVCName(b *mariadbv1.GaleraBackup) string {
	return "mysql-backup-" + b.Name
}

// BackupTransferPVCName returns the name of the PVC resource that stores temporary binary backup data
func BackupTransferPVCName(b *mariadbv1.GaleraBackup) string {
	return "mysql-transfer-" + b.Name
}

// RestorePodName - name of the pod resource created by a GaleraRestore CR
func RestorePodName(r *mariadbv1.GaleraRestore) string {
	return "restore-" + r.Name
}

// RestorePodLabels - common labels to map a pod to its associated GaleraRestore CR
func RestorePodLabels(r *mariadbv1.GaleraRestore) map[string]string {
	return labels.GetLabels(r, "galerarestore", map[string]string{})
}
