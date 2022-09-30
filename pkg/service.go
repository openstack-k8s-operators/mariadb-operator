package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net"
)

// Service func
func Service(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.Service {
	adoptionHost := db.Spec.AdoptionRedirect.Host
	adoptionHostIsIP := adoptionHost == "" || net.ParseIP(adoptionHost) != nil

	if adoptionHost != "" {
		if adoptionHostIsIP {
			return externalServiceFromIP(db, scheme)
		}
		return externalServiceFromName(db, scheme)
	}
	return internalService(db, scheme)
}

func internalService(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.Service {
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
	return svc
}

func externalServiceFromIP(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Type:      corev1.ServiceTypeClusterIP,
		},
	}
	return svc
}

func externalServiceFromName(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: corev1.ServiceSpec{
			ExternalName: db.Spec.AdoptionRedirect.Host,
			Type:         corev1.ServiceTypeExternalName,
		},
	}
	return svc
}
