package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func Pod(db *databasev1beta1.MariaDB, scheme *runtime.Scheme, configHash string) *corev1.Pod {

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "mariadb",
			Containers: []corev1.Container{
				{
					Name:  "mariadb",
					Image: db.Spec.ContainerImage,
					Env: []corev1.EnvVar{
						{
							Name:  "KOLLA_CONFIG_STRATEGY",
							Value: "COPY_ALWAYS",
						},
						{
							Name:  "CONFIG_HASH",
							Value: configHash,
						},
					},
					VolumeMounts: getVolumeMounts(),
				},
			},
			InitContainers: []corev1.Container{
				{
					Name:  "mariadb-init",
					Image: db.Spec.ContainerImage,
					Env: []corev1.EnvVar{
						{
							Name:  "KOLLA_CONFIG_STRATEGY",
							Value: "COPY_ALWAYS",
						},
						{
							Name:  "KOLLA_BOOTSTRAP",
							Value: "true",
						},

						{
							Name:  "DB_MAX_TIMEOUT",
							Value: "30",
						},
						{
							Name:  "DB_ROOT_PASSWORD",
							Value: db.Spec.RootPassword,
						},
					},
					VolumeMounts: getInitVolumeMounts(),
				},
			},
			Volumes: getVolumes(db.Name),
		},
	}
	controllerutil.SetControllerReference(db, pod, scheme)
	return pod
}
