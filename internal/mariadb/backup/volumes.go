package mariadbbackup

import (
	tls "github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	// GaleraCertPrefix is the prefix used for Galera certificate volume names
	GaleraCertPrefix = "galera"
)

// BackupVolumes returns the volumes needed for the backup job
func BackupVolumes(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.Volume {
	volumes := []corev1.Volume{{
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

	if b.Spec.TransferStorage != nil {
		logicalPVC := corev1.Volume{
			Name: "transfer-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: BackupTransferPVCName(b, g),
				},
			},
		}
		volumes = append(volumes, logicalPVC)
	}

	volumesTLS := []corev1.Volume{{
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
	}}

	if g.Spec.TLS.Enabled() {
		svc := tls.Service{
			SecretName: *g.Spec.TLS.SecretName,
			CertMount:  nil,
			KeyMount:   nil,
			CaMount:    nil,
		}
		serviceVolume := svc.CreateVolume(GaleraCertPrefix)
		volumes = append(volumes, serviceVolume)
		if g.Spec.TLS.CaBundleSecretName != "" {
			caVolume := g.Spec.TLS.CreateVolume()
			volumes = append(volumes, caVolume)
			volumes = append(volumes, volumesTLS...)
		}
	}

	return volumes
}

// BackupVolumeMounts returns the volume mounts needed for the backup job
func BackupVolumeMounts(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{{
		MountPath: "/var/lib/kolla/config_files",
		ReadOnly:  true,
		Name:      "kolla-config",
	}, {
		MountPath: "/var/lib/backup-scripts",
		ReadOnly:  true,
		Name:      "backup-scripts",
	}}

	var volumeMountsPVC []corev1.VolumeMount
	if b.Spec.TransferStorage != nil {
		volumeMountsPVC = []corev1.VolumeMount{{
			MountPath: "/backup/transfer",
			Name:      "transfer-data",
		}, {
			MountPath: "/backup/data",
			Name:      "backup-data",
		}}
	} else {
		volumeMountsPVC = []corev1.VolumeMount{{
			MountPath: "/backup",
			Name:      "backup-data",
		}}
	}
	volumeMounts = append(volumeMounts, volumeMountsPVC...)

	volumeMountsTLS := []corev1.VolumeMount{{
		MountPath: "/var/lib/config-data/default",
		ReadOnly:  true,
		Name:      "config-data",
	}}

	if g.Spec.TLS.Enabled() {
		svc := tls.Service{
			SecretName: *g.Spec.TLS.SecretName,
			CertMount:  nil,
			KeyMount:   nil,
			CaMount:    nil,
		}
		serviceVolumeMounts := svc.CreateVolumeMounts(GaleraCertPrefix)
		volumeMounts = append(volumeMounts, serviceVolumeMounts...)
		if g.Spec.TLS.CaBundleSecretName != "" {
			caVolumeMounts := g.Spec.TLS.CreateVolumeMounts(nil)
			volumeMounts = append(volumeMounts, caVolumeMounts...)
			volumeMounts = append(volumeMounts, volumeMountsTLS...)
		}
	}

	return volumeMounts
}
