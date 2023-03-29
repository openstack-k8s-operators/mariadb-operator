package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Pod -
func Pod(db *databasev1beta1.MariaDB, configHash string) *corev1.Pod {

	runAsUser := int64(0)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb-" + db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "mariadb-operator-mariadb",
			Containers: []corev1.Container{
				{
					Name:  "mariadb",
					Image: db.Spec.ContainerImage,
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: &runAsUser,
					},
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
			Volumes: getVolumes(db.Name),
		},
	}
	return pod
}
