package mariadb

import (
	"net"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Endpoints func
func Endpoints(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.Endpoints {
	adoptionHost := db.Spec.AdoptionRedirect.Host
	// We only create Endpoints directly if the adoption host is
	// defined and it has an IP format (not FQDN format).
	if adoptionHost == "" || net.ParseIP(adoptionHost) == nil {
		return nil
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: adoptionHost},
				},
			},
		},
	}
	return endpoints
}
