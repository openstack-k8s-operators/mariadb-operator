package mariadb

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Service(db metav1.Object) *corev1.Service {
	selectors := LabelSelectors(db, "galera")
	// NOTE(dciabrin) we currently deploy the Galera cluster as A/P,
	// by configuring the service's label selector to create
	// a single endpoint matching a single pod's name.
	// This label is later updated by a script called by Galera any
	// time the cluster's state changes.
	// When the service CR is being created, it is configured to
	// point to the first pod. This is fair enough as:
	//  1. when there's no pod running, there's no service anyway
	//  2. As soon as a galera node becomes available, the label will
	//     be reconfigured by the script if needed.
	//  3. If the Galera cluster is already running, picking a random
	//     node out of the running pods will work because Galera is
	//     a multi-master service.
	//  4. If the service CR gets deleted for whatever reason, and the
	//     cluster is still running, picking a random node out of the
	//     running pods will work because Galera is a multi-master
	//     service. This is true as long the first pod is not in a
	//     network partition without quorum.
	//     TODO improve that fallback pod selection
	selectors[ActivePodSelectorKey] = db.GetName() + "-galera-0"
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.GetName(),
			Namespace: db.GetNamespace(),
			Labels:    ServiceLabels(db),
		},
		Spec: corev1.ServiceSpec{
			Selector: selectors,
			Ports: []corev1.ServicePort{
				{Name: "database", Port: 3306, Protocol: corev1.ProtocolTCP},
			},
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
