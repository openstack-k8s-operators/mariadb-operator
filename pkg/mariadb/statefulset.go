package mariadb

import (
	"strconv"

	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// StatefulSet returns a StatefulSet object for the galera cluster
func StatefulSet(g *mariadbv1.Galera, configHash string, topology *topologyv1.Topology) (*appsv1.StatefulSet, error) {
	ls := StatefulSetLabels(g)
	name := StatefulSetName(g.Name)
	var replicas *int32
	if g.Status.StopRequired {
		replicas = ptr.To[int32](0)
	} else {
		replicas = g.Spec.Replicas
	}
	storage := g.Spec.StorageClass
	storageRequest, err := resource.ParseQuantity(g.Spec.StorageRequest)
	if err != nil {
		return nil, err
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: g.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: g.RbacResourceName(),
					InitContainers:     getGaleraInitContainers(g),
					Containers:         getGaleraContainers(g, configHash),
					Volumes:            getGaleraVolumes(g),
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
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								"storage": storageRequest,
							},
						},
					},
				},
			},
		},
	}
	if g.Spec.NodeSelector != nil {
		sts.Spec.Template.Spec.NodeSelector = *g.Spec.NodeSelector
	}
	if topology != nil {
		topology.ApplyTo(&sts.Spec.Template)
	} else {
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
	}
	return sts, nil
}

func getGaleraInitContainers(g *mariadbv1.Galera) []corev1.Container {
	return []corev1.Container{{
		Image:   g.Spec.ContainerImage,
		Name:    "mysql-bootstrap",
		Command: []string{"bash", "/var/lib/operator-scripts/mysql_bootstrap.sh"},
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
		Resources:    g.Spec.Resources,
		VolumeMounts: getGaleraInitVolumeMounts(g),
	}}
}

func getGaleraContainers(g *mariadbv1.Galera, configHash string) []corev1.Container {
	timeout := strconv.Itoa(StartupProbeTimeout)
	containers := []corev1.Container{{
		Image:   g.Spec.ContainerImage,
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
		Ports: []corev1.ContainerPort{{
			ContainerPort: 3306,
			Name:          "mysql",
		}, {
			ContainerPort: 4567,
			Name:          "galera",
		}},
		Resources:    g.Spec.Resources,
		VolumeMounts: getGaleraVolumeMounts(g),
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/bash", "/var/lib/operator-scripts/mysql_probe.sh", "startup", timeout},
				},
			},
			// extra seconds so that the script is not preempted by k8s
			TimeoutSeconds: StartupProbeTimeout + 10,
			// the current probe implementation assumes a single failure threshold
			FailureThreshold: 1,
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
		Lifecycle: &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/bash", "/var/lib/operator-scripts/mysql_shutdown.sh"},
				},
			},
		},
	}}
	logSideCar := corev1.Container{
		Image:        g.Spec.ContainerImage,
		Name:         "log",
		Command:      []string{"/usr/bin/dumb-init", "--", "/bin/sh", "-c", "tail -n+1 -F /var/log/mariadb/mariadb.log"},
		VolumeMounts: getGaleraVolumeMounts(g),
	}

	if g.Spec.LogToDisk {
		containers = append(containers, logSideCar)
	}

	return containers
}
