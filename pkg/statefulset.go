package mariadb

import (
	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatefulSet returns a StatefulSet object for the galera cluster
func StatefulSet(g *mariadbv1.Galera) *appsv1.StatefulSet {
	ls := StatefulSetLabels(g)
	name := StatefulSetName(g.Name)
	replicas := g.Spec.Replicas
	runAsUser := int64(0)
	storage := g.Spec.StorageClass
	storageRequest := resource.MustParse(g.Spec.StorageRequest)
	configHash := g.Status.ConfigHash
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: g.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "mariadb-operator-mariadb",
					InitContainers: []corev1.Container{{
						Image:   g.Spec.ContainerImage,
						Name:    "mysql-bootstrap",
						Command: []string{"bash", "/var/lib/operator-scripts/mysql_bootstrap.sh"},
						SecurityContext: &corev1.SecurityContext{
							RunAsUser: &runAsUser,
						},
						Env: []corev1.EnvVar{{
							Name:  "KOLLA_BOOTSTRAP",
							Value: "True",
						}, {
							Name:  "KOLLA_CONFIG_STRATEGY",
							Value: "COPY_ALWAYS",
						}, {
							Name: "DB_ROOT_PASSWORD",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: g.Spec.Secret,
									},
									Key: "DbRootPassword",
								},
							},
						}},
						VolumeMounts: []corev1.VolumeMount{{
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
							MountPath: "/var/lib/operator-scripts",
							ReadOnly:  true,
							Name:      "operator-scripts",
						}, {
							MountPath: "/var/lib/kolla/config_files",
							ReadOnly:  true,
							Name:      "kolla-config",
						}},
					}},
					Containers: []corev1.Container{{
						Image: g.Spec.ContainerImage,
						// ImagePullPolicy: "Always",
						Name:    "galera",
						Command: []string{"/usr/bin/dumb-init", "--", "/usr/local/bin/kolla_start"},
						Env: []corev1.EnvVar{{
							Name:  "CR_CONFIG_HASH",
							Value: configHash,
						}, {
							Name:  "KOLLA_CONFIG_STRATEGY",
							Value: "COPY_ALWAYS",
						}, {
							Name: "DB_ROOT_PASSWORD",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: g.Spec.Secret,
									},
									Key: "DbRootPassword",
								},
							},
						}},
						SecurityContext: &corev1.SecurityContext{
							RunAsUser: &runAsUser,
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 3306,
							Name:          "mysql",
						}, {
							ContainerPort: 4567,
							Name:          "galera",
						}},
						VolumeMounts: []corev1.VolumeMount{{
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
						}},
						StartupProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{
									Command: []string{"/bin/bash", "/var/lib/operator-scripts/mysql_probe.sh", "startup"},
								},
							},
							PeriodSeconds:    10,
							FailureThreshold: 30,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{
									Command: []string{"/bin/bash", "/var/lib/operator-scripts/mysql_probe.sh", "liveness"},
								},
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{
									Command: []string{"/bin/bash", "/var/lib/operator-scripts/mysql_probe.sh", "readiness"},
								},
							},
						},
					}},
					Volumes: []corev1.Volume{
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
									Items: []corev1.KeyToPath{
										{
											Key:  "galera.cnf.in",
											Path: "galera.cnf.in",
										},
										{
											Key:  mariadbv1.CustomServiceConfigFile,
											Path: mariadbv1.CustomServiceConfigFile,
										},
									},
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
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "mysql-db",
						Labels: ls,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							"ReadWriteOnce",
						},
						StorageClassName: &storage,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								"storage": storageRequest,
							},
						},
					},
				},
			},
		},
	}

	// If possible two pods of the same service should not
	// run on the same worker node. If this is not possible
	// the get still created on the same worker node.
	sts.Spec.Template.Spec.Affinity = affinity.DistributePods(
		common.AppSelector,
		[]string{
			name,
		},
		corev1.LabelHostname,
	)
	if g.Spec.NodeSelector != nil && len(g.Spec.NodeSelector) > 0 {
		sts.Spec.Template.Spec.NodeSelector = g.Spec.NodeSelector
	}

	return sts
}
