package mariadb

import (
	tls "github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

const (
	GaleraCertPrefix = "galera"
)

func getGaleraVolumes(g *mariadbv1.Galera) []corev1.Volume {
	configTemplates := []corev1.KeyToPath{
		{
			Key:  "galera.cnf.in",
			Path: "galera.cnf.in",
		},
		{
			Key:  mariadbv1.CustomServiceConfigFile,
			Path: mariadbv1.CustomServiceConfigFile,
		},
	}

	if g.Spec.TLS.Enabled() {
		if g.Spec.TLS.Ca.CaBundleSecretName != "" {
			configTemplates = append(configTemplates, corev1.KeyToPath{
				Key:  "galera_tls.cnf.in",
				Path: "galera_tls.cnf.in",
			})
		} else {
			// Without a CA, WSREP is unencrypted. Only SQL traffic is.
			configTemplates = append(configTemplates, corev1.KeyToPath{
				Key:  "galera_external_tls.cnf.in",
				Path: "galera_external_tls.cnf.in",
			})
		}
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
			Name: "config-data-generated",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "config-data-default",
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
		}
	}

	return volumes
}

func getGaleraVolumeMounts(g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{
		{
			MountPath: "/var/lib/mysql",
			Name:      "mysql-db",
		}, {
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
			Name:      "config-data-default",
		}, {
			MountPath: "/var/lib/config-data/generated",
			Name:      "config-data-generated",
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
		}
	}

	return volumeMounts
}

func getGaleraInitVolumeMounts(g *mariadbv1.Galera) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{
		{
			MountPath: "/var/lib/mysql",
			Name:      "mysql-db",
		}, {
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
			Name:      "config-data-default",
		}, {
			MountPath: "/var/lib/config-data/generated",
			Name:      "config-data-generated",
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
