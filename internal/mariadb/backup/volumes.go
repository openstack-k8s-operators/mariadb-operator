package mariadbbackup

import (
	tls "github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	// GaleraCertPrefix is the prefix used for Galera certificate volume names
	GaleraCertPrefix = "galera"
)

// baseVolumes returns the volumes shared by both backup and restore pods:
// kolla-config, operator-scripts, backup-scripts, and backup-data PVC.
func baseVolumes(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.Volume {
	return []corev1.Volume{{
		Name: "kolla-config",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: b.Name + "-backup-config",
				},
				Items: []corev1.KeyToPath{
					{
						Key:  "backup-config.json",
						Path: "config.json",
					},
				},
			},
		},
	}, {
		Name: "operator-scripts",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: mariadb.ScriptConfigMapName(g.Name),
				},
				Items: []corev1.KeyToPath{
					{
						Key:  "mysql_root_auth.sh",
						Path: "mysql_root_auth.sh",
					},
				},
			},
		},
	}, {
		Name: "backup-scripts",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: b.Name + "-backup-scripts",
				},
				Items: []corev1.KeyToPath{
					{
						Key:  "backup_galera",
						Path: "backup_galera",
					},
					{
						Key:  "restore_galera",
						Path: "restore_galera",
					},
				},
				DefaultMode: ptr.To[int32](0555),
			},
		},
	}, {
		Name: "backup-data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: BackupPVCName(b, g),
			},
		},
	}}
}

// tlsVolumes returns TLS-related volumes when TLS is enabled.
func tlsVolumes(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.Volume {
	var volumes []corev1.Volume
	if g.Spec.TLS.Enabled() {
		svc := tls.Service{
			SecretName: *g.Spec.TLS.SecretName,
			CertMount:  nil,
			KeyMount:   nil,
			CaMount:    nil,
		}
		volumes = append(volumes, svc.CreateVolume(GaleraCertPrefix))
		if g.Spec.TLS.CaBundleSecretName != "" {
			volumes = append(volumes, g.Spec.TLS.CreateVolume())
			volumes = append(volumes, corev1.Volume{
				Name: "config-data",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: b.Name + "-backup-config",
						},
						Items: []corev1.KeyToPath{{
							Key:  "mysql_backup_tls.cnf",
							Path: "mysql_backup_tls.cnf",
						}, {
							Key:  "garbd_backup_tls.cnf",
							Path: "garbd_backup_tls.cnf",
						}},
					},
				},
			})
		}
	}
	return volumes
}

// baseVolumeMounts returns the volume mounts shared by both backup and restore pods:
// kolla-config, operator-scripts, and backup-scripts.
func baseVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{{
		MountPath: "/var/lib/kolla/config_files",
		ReadOnly:  true,
		Name:      "kolla-config",
	}, {
		MountPath: "/var/lib/operator-scripts",
		ReadOnly:  true,
		Name:      "operator-scripts",
	}, {
		MountPath: "/var/lib/backup-scripts",
		ReadOnly:  true,
		Name:      "backup-scripts",
	}}
}

// tlsVolumeMounts returns TLS-related volume mounts when TLS is enabled.
func tlsVolumeMounts(g *mariadbv1.Galera) []corev1.VolumeMount {
	var mounts []corev1.VolumeMount
	if g.Spec.TLS.Enabled() {
		svc := tls.Service{
			SecretName: *g.Spec.TLS.SecretName,
			CertMount:  nil,
			KeyMount:   nil,
			CaMount:    nil,
		}
		mounts = append(mounts, svc.CreateVolumeMounts(GaleraCertPrefix)...)
		if g.Spec.TLS.CaBundleSecretName != "" {
			mounts = append(mounts, g.Spec.TLS.CreateVolumeMounts(nil)...)
			mounts = append(mounts, corev1.VolumeMount{
				MountPath: "/var/lib/config-data/default",
				ReadOnly:  true,
				Name:      "config-data",
			})
		}
	}
	return mounts
}

// BackupVolumes returns the volumes needed for the backup job
func BackupVolumes(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.Volume {
	volumes := baseVolumes(b, g)

	if b.Spec.TransferStorage != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "transfer-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: BackupTransferPVCName(b, g),
				},
			},
		})
	}

	volumes = append(volumes, tlsVolumes(b, g)...)
	return volumes
}

// RestoreVolumes returns the volumes needed for the restore pod.
// Unlike BackupVolumes, this excludes the transfer-data volume since
// the restore process only reads SQL dumps from the backup PVC.
// This avoids scheduling conflicts with local storage (LVMS/TopoLVM)
// where the transfer PVC could land on a different node.
func RestoreVolumes(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.Volume {
	volumes := baseVolumes(b, g)
	volumes = append(volumes, tlsVolumes(b, g)...)
	return volumes
}

// BackupVolumeMounts returns the volume mounts needed for the backup job
func BackupVolumeMounts(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := baseVolumeMounts()

	if b.Spec.TransferStorage != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			MountPath: "/backup/transfer",
			Name:      "transfer-data",
		}, corev1.VolumeMount{
			MountPath: "/backup/data",
			Name:      "backup-data",
		})
	} else {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			MountPath: "/backup",
			Name:      "backup-data",
		})
	}

	volumeMounts = append(volumeMounts, tlsVolumeMounts(g)...)
	return volumeMounts
}

// RestoreVolumeMounts returns the volume mounts needed for the restore pod.
// The backup PVC is mounted at the same path as in the backup pod so that
// dump files are found at the expected location. When transfer storage is
// configured, backup-data is at /backup/data; otherwise it is at /backup.
func RestoreVolumeMounts(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := baseVolumeMounts()

	if b.Spec.TransferStorage != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			MountPath: "/backup/data",
			Name:      "backup-data",
		})
	} else {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			MountPath: "/backup",
			Name:      "backup-data",
		})
	}

	volumeMounts = append(volumeMounts, tlsVolumeMounts(g)...)
	return volumeMounts
}
