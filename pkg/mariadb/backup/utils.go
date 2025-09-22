package mariadbbackup

import mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"

// BackupCronJobName - name of the cronjob resource created for a backup CR
func BackupCronJobName(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) string {
	return g.Name + "-backup-" + b.Name
}

// BackupPVCName returns the name of the PVC resource that stores .sql backup files
func BackupPVCName(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) string {
	return "mysql-db-" + g.Name + "-backup-" + b.Name
}

// BackupTransferPVCName returns the name of the PVC resource that stores temporary binary backup data
func BackupTransferPVCName(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) string {
	return "mysql-db-" + g.Name + "-backup-" + b.Name + "-transfer"
}
