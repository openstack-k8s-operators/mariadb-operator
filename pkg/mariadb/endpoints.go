package mariadb

import (
	"net"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EndpointsForAdoption - create an endpoint based on the adoption configuration
func EndpointsForAdoption(db metav1.Object, adoption *databasev1beta1.AdoptionRedirectSpec) *corev1.Endpoints {
	adoptionHost := adoption.Host
	// We only create Endpoints directly if the adoption host is
	// defined and it has an IP format (not FQDN format).
	if adoptionHost == "" || net.ParseIP(adoptionHost) == nil {
		return nil
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.GetName(),
			Namespace: db.GetNamespace(),
			Labels:    GetLabels(db.GetName()),
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
