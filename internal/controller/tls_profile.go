package controller

import (
	"context"

	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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
