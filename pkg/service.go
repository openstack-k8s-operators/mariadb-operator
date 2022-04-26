package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Service func
func Service(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) (*corev1.Service, error) {

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "mariadb"},
			Ports: []corev1.ServicePort{
				{Name: "database", Port: 3306, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	err := controllerutil.SetControllerReference(db, svc, scheme)
	return svc, err
}
