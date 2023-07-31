package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Pvc - Returns the deployment object for the Database
func Pvc(db *databasev1beta1.MariaDB) *corev1.PersistentVolumeClaim {
	pv := &corev1.PersistentVolumeClaim{
		ObjectMeta: v1.ObjectMeta{
			Name:      "mariadb-" + db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(db.Spec.StorageRequest),
				},
			},
			StorageClassName: &db.Spec.StorageClass,
		},
	}
	return pv
}
