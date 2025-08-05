package mariadb_backup

import (
	tls "github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

const (
	GaleraCertPrefix = "galera"
)

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
						Key:  "backup_galera.sh",
						Path: "backup_galera.sh",
					},
				},
			},
		},
	}, {
		Name: "backup-data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: BackupPhysicalPVCName(b, g),
			},
		},
	}}

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
			SecretName: *g.Spec.TLS.GenericService.SecretName,
			CertMount:  nil,
			KeyMount:   nil,
			CaMount:    nil,
		}
		serviceVolume := svc.CreateVolume(GaleraCertPrefix)
		volumes = append(volumes, serviceVolume)
		if g.Spec.TLS.Ca.CaBundleSecretName != "" {
			caVolume := g.Spec.TLS.Ca.CreateVolume()
			volumes = append(volumes, caVolume)
			volumes = append(volumes, volumesTLS...)
		}
	}

	return volumes
}

func BackupVolumeMounts(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{{
		MountPath: "/var/lib/kolla/config_files",
		ReadOnly:  true,
		Name:      "kolla-config",
	}, {
		MountPath: "/var/lib/backup-scripts",
		ReadOnly:  true,
		Name:      "backup-scripts",
	}, {
		MountPath: "/mysql",
		Name:      "backup-data",
	}}

	volumeMountsTLS := []corev1.VolumeMount{{
		MountPath: "/var/lib/config-data/default",
		ReadOnly:  true,
		Name:      "config-data",
	}}

	if g.Spec.TLS.Enabled() {
		svc := tls.Service{
			SecretName: *g.Spec.TLS.GenericService.SecretName,
			CertMount:  nil,
			KeyMount:   nil,
			CaMount:    nil,
		}
		serviceVolumeMounts := svc.CreateVolumeMounts(GaleraCertPrefix)
		volumeMounts = append(volumeMounts, serviceVolumeMounts...)
		if g.Spec.TLS.Ca.CaBundleSecretName != "" {
			caVolumeMounts := g.Spec.TLS.Ca.CreateVolumeMounts(nil)
			volumeMounts = append(volumeMounts, caVolumeMounts...)
			volumeMounts = append(volumeMounts, volumeMountsTLS...)
		}
	}

	return volumeMounts
}
