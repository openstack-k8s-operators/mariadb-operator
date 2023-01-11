package mariadb

import (
	"fmt"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	annotations "github.com/openstack-k8s-operators/lib-common/modules/common/annotations"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

// Pod -
func Pod(db *databasev1beta1.MariaDB, configHash string) (*corev1.Pod, error) {

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

	// networks to attach to
	nwAnnotation, err := annotations.GetNADAnnotation(db.Namespace, db.Spec.NetworkAttachmentDefinitions)
	if err != nil {
		return nil, fmt.Errorf("failed create network annotation from %s: %w",
			db.Spec.NetworkAttachmentDefinitions, err)
	}
	pod.Annotations = util.MergeStringMaps(pod.Annotations, nwAnnotation)

	return pod, nil
}
