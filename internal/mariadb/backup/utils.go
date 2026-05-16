package mariadbbackup

import (
	"crypto/sha256"
	"fmt"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

const (
	// backupJobMaxLabelLen is the maximum CronJob name length that keeps
	// auto-generated Job names within the 63-character Kubernetes label
	// value limit. We reserve 16 characters for the timestamp suffix
	// appended by the CronJob controller or backup scripts.
	backupJobMaxLabelLen = 63 - 16
)

// BackupCronJobName - name of the cronjob resource created for a backup CR.
// If the combined name exceeds the label value limit, it is truncated and
// a short hash is appended to preserve uniqueness.
func BackupCronJobName(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) string {
	name := g.Name + "-backup-" + b.Name
	return truncateWithHash(name, backupJobMaxLabelLen)
}

// truncateWithHash returns name unchanged if it fits within maxLen.
// Otherwise it truncates and appends a short SHA256 hash for uniqueness.
func truncateWithHash(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	hash := sha256.Sum256([]byte(name))
	hashHex := fmt.Sprintf("%x", hash[:4])
	return name[:maxLen-len(hashHex)] + hashHex
}

// BackupPVCName returns the name of the PVC resource that stores .sql backup files
func BackupPVCName(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) string {
	return "mysql-backup-" + g.Name + "-backup-" + b.Name
}

// BackupTransferPVCName returns the name of the PVC resource that stores temporary binary backup data
func BackupTransferPVCName(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) string {
	return "mysql-transfer-" + g.Name + "-backup-" + b.Name
}

// RestorePodName - name of the pod resource created by a GaleraRestore CR
func RestorePodName(r *mariadbv1.GaleraRestore, g *mariadbv1.Galera) string {
	return g.Name + "-restore-" + r.Name
}
