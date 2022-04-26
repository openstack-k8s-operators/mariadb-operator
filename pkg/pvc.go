package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Pvc - Returns the deployment object for the Database
func Pvc(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) (*corev1.PersistentVolumeClaim, error) {
	pv := &corev1.PersistentVolumeClaim{
		ObjectMeta: v1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &db.Spec.StorageClass,
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(db.Spec.StorageRequest),
				},
			},
		},
	}
	err := controllerutil.SetControllerReference(db, pv, scheme)
	return pv, err
}
