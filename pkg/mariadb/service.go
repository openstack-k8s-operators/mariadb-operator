package mariadb

import (
	"net"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceForAdoption - create a service based on the adoption configuration
func ServiceForAdoption(db metav1.Object, dbType string, adoption *databasev1beta1.AdoptionRedirectSpec) *corev1.Service {
	adoptionHost := adoption.Host
	adoptionHostIsIP := adoptionHost == "" || net.ParseIP(adoptionHost) != nil

	if adoptionHost != "" {
		if adoptionHostIsIP {
			return externalServiceFromIP(db, adoption)
		}
		return externalServiceFromName(db, adoption)
	}
	return internalService(db, dbType, adoption)
}

func internalService(db metav1.Object, dbType string, adoption *databasev1beta1.AdoptionRedirectSpec) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.GetName(),
			Namespace: db.GetNamespace(),
			Labels:    ServiceLabels(db),
		},
		Spec: corev1.ServiceSpec{
			Selector: LabelSelectors(db, dbType),
			Ports: []corev1.ServicePort{
				{Name: "database", Port: 3306, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	return svc
}

func externalServiceFromIP(db metav1.Object, adoption *databasev1beta1.AdoptionRedirectSpec) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.GetName(),
			Namespace: db.GetNamespace(),
			Labels:    ServiceLabels(db),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Type:      corev1.ServiceTypeClusterIP,
		},
	}
	return svc
}

func externalServiceFromName(db metav1.Object, adoption *databasev1beta1.AdoptionRedirectSpec) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.GetName(),
			Namespace: db.GetNamespace(),
			Labels:    ServiceLabels(db),
		},
		Spec: corev1.ServiceSpec{
			ExternalName: adoption.Host,
			Type:         corev1.ServiceTypeExternalName,
		},
	}
	return svc
}

// HeadlessService - service to give galera pods connectivity via DNS
func HeadlessService(db metav1.Object) *corev1.Service {
	name := ResourceName(db.GetName())
	dep := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: db.GetNamespace(),
		},
		Spec: corev1.ServiceSpec{
			Type:      "ClusterIP",
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{Name: "mysql", Protocol: "TCP", Port: 3306},
			},
			Selector: LabelSelectors(db, "galera"),
			// This is required to let pod communicate when
			// they are still in Starting state
			PublishNotReadyAddresses: true,
		},
	}
	return dep
}
