package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

func getVolumes(instance *databasev1beta1.MariaDB) []corev1.Volume {
	var config0600AccessMode int32 = 0600
	var volumes = []corev1.Volume{
		{
			Name: "kolla-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "mariadb-" + instance.Name,
					},
					Items: []corev1.KeyToPath{
						{
							Key:  "config.json",
							Path: "config.json",
						},
					},
				},
			},
		},
		{
			Name: "kolla-config-init",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "mariadb-" + instance.Name,
					},
					Items: []corev1.KeyToPath{
						{
							Key:  "init_config.json",
							Path: "config.json",
						},
					},
				},
			},
		},
		{
			Name: "config-data",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "mariadb-" + instance.Name,
					},
					Items: []corev1.KeyToPath{
						{
							Key:  "galera.cnf",
							Path: "galera.cnf",
						},
						{
							Key:  "mariadb_init.sh",
							Path: "mariadb_init.sh",
						},
					},
				},
			},
		},
		{
			Name: "lib-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "mariadb-" + instance.Name,
				},
			},
		},
	}

	if instance.Spec.TLS.SecretName != "" {
		volumes = append(
			volumes,
			corev1.Volume{
				Name: "tls-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						DefaultMode: &config0600AccessMode,
						SecretName:  instance.Spec.TLS.SecretName,
					},
				},
			},
		)
	}

	if instance.Spec.TLS.CaSecretName != "" {
		volumes = append(
			volumes,
			corev1.Volume{
				Name: "tlsca-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						DefaultMode: &config0600AccessMode,
						SecretName:  instance.Spec.TLS.CaSecretName,
					},
				},
			},
		)
	}

	return volumes
}

func getVolumeMounts(instance *databasev1beta1.MariaDB) []corev1.VolumeMount {
	var volumeMounts = []corev1.VolumeMount{
		{
			MountPath: "/var/lib/config-data",
			ReadOnly:  true,
			Name:      "config-data",
		},
		{
			MountPath: "/var/lib/kolla/config_files",
			ReadOnly:  true,
			Name:      "kolla-config",
		},
		{
			MountPath: "/var/lib/mysql",
			ReadOnly:  false,
			Name:      "lib-data",
		},
	}

	if instance.Spec.TLS.SecretName != "" {
		volumeMounts = append(
			volumeMounts,
			corev1.VolumeMount{
				Name:      "tls-secret",
				MountPath: "/var/lib/tls-data",
				ReadOnly:  true,
			},
		)
	}

	if instance.Spec.TLS.CaSecretName != "" {
		volumeMounts = append(
			volumeMounts,
			corev1.VolumeMount{
				Name:      "tlsca-secret",
				MountPath: "/var/lib/tlsca-data",
				ReadOnly:  true,
			},
		)
	}

	return volumeMounts
}

func getInitVolumeMounts(instance *databasev1beta1.MariaDB) []corev1.VolumeMount {
	var volumeMounts = []corev1.VolumeMount{
		{
			MountPath: "/var/lib/config-data",
			ReadOnly:  true,
			Name:      "config-data",
		},
		{
			MountPath: "/var/lib/kolla/config_files",
			ReadOnly:  true,
			Name:      "kolla-config-init",
		},
		{
			MountPath: "/var/lib/mysql",
			ReadOnly:  false,
			Name:      "lib-data",
		},
	}
	return volumeMounts
}
