package controller

import (
	"context"
	"strings"

	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Replication peers with disjoint TLS policies require a coordinated restart.
// SQL-only TLS can continue using the StatefulSet's normal rolling updates.
func replicationTLSProfileHash(ctx context.Context, c client.Client, instance *mariadbv1.Galera) (string, error) {
	if !instance.Spec.TLS.Enabled() || instance.Spec.TLS.CaBundleSecretName == "" {
		return "", nil
	}
	config := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Name: configMapNameForConfig(instance), Namespace: instance.Namespace}, config); err != nil {
		return "", err
	}
	policy := config.Data[mariadb.TLSProfileConfigFile]
	if !strings.Contains(policy, "[crypto_policy]") {
		return "", nil
	}
	return util.ObjectHash(policy)
}

// The profile is owned by openstack-operator, so Owns(ConfigMap) does not
// enqueue its consumers. Watch it explicitly, including creation and deletion.
func tlsProfileRequests(ctx context.Context, c client.Client, source client.Object, list client.ObjectList) []reconcile.Request {
	if source.GetName() != util.TLSProfileConfigMap {
		return nil
	}
	if err := c.List(ctx, list, client.InNamespace(source.GetNamespace())); err != nil {
		log.FromContext(ctx).Error(err, "Unable to list TLS profile consumers")
		return nil
	}
	var requests []reconcile.Request
	err := meta.EachListItem(list, func(obj runtime.Object) error {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return err
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{Namespace: accessor.GetNamespace(), Name: accessor.GetName()},
		})
		return nil
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "Unable to read TLS profile consumers")
	}
	return requests
}

// Backups inherit the referenced Galera's TLS overrides as well as the profile.
func galeraBackupRequests(ctx context.Context, c client.Client, source client.Object) []reconcile.Request {
	backups := &mariadbv1.GaleraBackupList{}
	if err := c.List(ctx, backups, client.InNamespace(source.GetNamespace())); err != nil {
		log.FromContext(ctx).Error(err, "Unable to list Galera backup consumers")
		return nil
	}
	var requests []reconcile.Request
	for _, backup := range backups.Items {
		if backup.Spec.DatabaseInstance == source.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&backup)})
		}
	}
	return requests
}
