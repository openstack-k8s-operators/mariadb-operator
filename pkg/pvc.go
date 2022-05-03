package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Pvc - Returns the deployment object for the Database
func Pvc(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.PersistentVolumeClaim {
	pv := &corev1.PersistentVolumeClaim{
		ObjectMeta: v1.ObjectMeta{
			Name:      "mariadb-" + db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
	}
	return pv
}
