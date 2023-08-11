package mariadb

import (
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

func getVolumes(name string) []corev1.Volume {

	return []corev1.Volume{

		{
			Name: "kolla-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "mariadb-" + name,
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
						Name: "mariadb-" + name,
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
						Name: "mariadb-" + name,
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
					ClaimName: "mariadb-" + name,
				},
			},
		},
	}

}

func getVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
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

}

func getInitVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
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

}

func getGaleraVolumes(g *mariadbv1.Galera) []corev1.Volume {
	configTemplates := []corev1.KeyToPath{
		{
			Key:  "my.cnf.in",
			Path: ".my.cnf.in",
		},
		{
			Key:  "galera.cnf.in",
			Path: "galera.cnf.in",
		},
		{
			Key:  mariadbv1.CustomServiceConfigFile,
			Path: mariadbv1.CustomServiceConfigFile,
		},
	}

	if g.Spec.SST == mariadbv1.MariaBackup {
		configTemplates = append(configTemplates, corev1.KeyToPath{
			Key:  "galera_sst_mariabackup.cnf.in",
			Path: "galera_sst_mariabackup.cnf.in",
		})
	}

	volumes := []corev1.Volume{
		{
			Name: "secrets",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: g.Spec.Secret,
					Items: []corev1.KeyToPath{
						{
							Key:  "DbRootPassword",
							Path: "dbpassword",
						},
					},
				},
			},
		},
		{
			Name: "kolla-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: g.Name + "-config-data",
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
						Name: g.Name + "-config-data",
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
			Name: "pod-config-data",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "config-data",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: g.Name + "-config-data",
					},
					Items: configTemplates,
				},
			},
		},
		{
			Name: "operator-scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: g.Name + "-scripts",
					},
					Items: []corev1.KeyToPath{
						{
							Key:  "mysql_bootstrap.sh",
							Path: "mysql_bootstrap.sh",
						},
						{
							Key:  "mysql_probe.sh",
							Path: "mysql_probe.sh",
						},
						{
							Key:  "detect_last_commit.sh",
							Path: "detect_last_commit.sh",
						},
						{
							Key:  "detect_gcomm_and_start.sh",
							Path: "detect_gcomm_and_start.sh",
						},
					},
				},
			},
		},
	}

	return volumes
}

func getGaleraVolumeMounts(g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{
		{
			MountPath: "/var/lib/mysql",
			Name:      "mysql-db",
		}, {
			MountPath: "/var/lib/config-data",
			ReadOnly:  true,
			Name:      "config-data",
		}, {
			MountPath: "/var/lib/pod-config-data",
			Name:      "pod-config-data",
		}, {
			MountPath: "/var/lib/secrets",
			ReadOnly:  true,
			Name:      "secrets",
		}, {
			MountPath: "/var/lib/operator-scripts",
			ReadOnly:  true,
			Name:      "operator-scripts",
		}, {
			MountPath: "/var/lib/kolla/config_files",
			ReadOnly:  true,
			Name:      "kolla-config",
		},
	}

	return volumeMounts
}

func getGaleraInitVolumeMounts(g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{
		{
			MountPath: "/var/lib/mysql",
			Name:      "mysql-db",
		}, {
			MountPath: "/var/lib/config-data",
			ReadOnly:  true,
			Name:      "config-data",
		}, {
			MountPath: "/var/lib/pod-config-data",
			Name:      "pod-config-data",
		}, {
			MountPath: "/var/lib/secrets",
			ReadOnly:  true,
			Name:      "secrets",
		}, {
			MountPath: "/var/lib/operator-scripts",
			ReadOnly:  true,
			Name:      "operator-scripts",
		}, {
			MountPath: "/var/lib/kolla/config_files",
			ReadOnly:  true,
			Name:      "kolla-config-init",
		},
	}

	return volumeMounts
}
