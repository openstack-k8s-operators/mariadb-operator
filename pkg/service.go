package mariadb

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

// Service func
func Service(cr *databasev1beta1.MariaDB, cmName string) *corev1.Service {

	labels := map[string]string{
		"app": "mariadb",
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "database", Port: 3306, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	return svc
}
