/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	job "github.com/openstack-k8s-operators/lib-common/modules/common/job"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg"
)

// MariaDBDatabaseReconciler reconciles a MariaDBDatabase object
type MariaDBDatabaseReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// GetClient -
func (r *MariaDBDatabaseReconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *MariaDBDatabaseReconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetLogger -
func (r *MariaDBDatabaseReconciler) GetLogger() logr.Logger {
	return r.Log
}

// GetScheme -
func (r *MariaDBDatabaseReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs/status,verbs=get;list
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/status,verbs=get;list
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;delete;patch

// Reconcile reconcile mariadbdatabase API requests
func (r *MariaDBDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	_ = r.Log.WithValues("mariadbdatabase", req.NamespacedName)

	// Fetch the MariaDBDatabase instance
	instance := &databasev1beta1.MariaDBDatabase{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	helper, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		r.Log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always patch the instance status when exiting this function so we can persist any changes.
	defer func() {
		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		return ctrl.Result{}, nil
	}

	// Fetch the Galera or MariaDB instance from which we'll pull the credentials
	// Note: this will go away when we transition to galera as the db
	var isGalera bool
	var db client.Object
	var dbgalera *databasev1beta1.Galera
	var dbmariadb *databasev1beta1.MariaDB
	var dbName string
	var dbSecret string
	var dbContainerImage string
	// Try to fetch Galera first
	dbgalera = &databasev1beta1.Galera{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.ObjectMeta.Labels["dbName"],
			Namespace: req.Namespace,
		},
	}
	objectKey := client.ObjectKeyFromObject(dbgalera)
	err = r.Client.Get(ctx, objectKey, dbgalera)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	db = dbgalera

	isGalera = err == nil
	if !isGalera {
		// Try to fetch MariaDB when Galera is not used
		dbmariadb = &databasev1beta1.MariaDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instance.ObjectMeta.Labels["dbName"],
				Namespace: req.Namespace,
			},
		}
		objectKey = client.ObjectKeyFromObject(dbmariadb)
		err = r.Client.Get(ctx, objectKey, dbmariadb)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		db = dbmariadb
	}

	if k8s_errors.IsNotFound(err) {
		// Handle special case for removing service finalizer in the context of this MariaDBDatabase
		// being deleted but Galera/MariaDB is not found
		if !instance.DeletionTimestamp.IsZero() {
			controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
		}
		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	if isGalera {
		dbName, dbSecret, dbContainerImage = dbgalera.Name, dbgalera.Spec.Secret, dbgalera.Spec.ContainerImage
	} else {
		dbName, dbSecret, dbContainerImage = dbmariadb.Name, dbmariadb.Spec.Secret, dbmariadb.Spec.ContainerImage
	}

	// Either add or remove the MariaDBDatabase finalizer for this instance to/from the associated Galera/MariaDB
	// instance based on whether this MariaDBDatabase is being deleted or not
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(db, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
		err := r.Update(ctx, db)

		if err != nil {
			return ctrl.Result{}, err
		}
	} else if !instance.DeletionTimestamp.IsZero() && controllerutil.RemoveFinalizer(db, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
		err := r.Update(ctx, db)

		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// If this MariaDBDatabase is being deleted, there's no reason to continue beyond making
	// sure the service finalizer has been removed
	if !instance.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
		return ctrl.Result{}, nil
	}

	//
	// Non-deletion (normal) flow follows
	//

	if isGalera {
		if !dbgalera.Status.Bootstrapped {
			r.Log.Info("DB bootstrap not complete. Requeue...")
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}
	} else if dbmariadb.Status.DbInitHash == "" {
		r.Log.Info("DB initialization not complete. Requeue...")
		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, err
	}

	// Define a new Job object (hostname, password, containerImage)
	jobDef, err := mariadb.DbDatabaseJob(instance, dbName, dbSecret, dbContainerImage)
	if err != nil {
		return ctrl.Result{}, err
	}
	dbCreateHash := instance.Status.Hash[databasev1beta1.DbCreateHash]
	dbCreateJob := job.NewJob(
		jobDef,
		databasev1beta1.DbCreateHash,
		false,
		time.Duration(5)*time.Second,
		dbCreateHash,
	)
	ctrlResult, err := dbCreateJob.DoJob(
		ctx,
		helper,
	)
	if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if dbCreateJob.HasChanged() {
		if instance.Status.Hash == nil {
			instance.Status.Hash = make(map[string]string)
		}
		instance.Status.Hash[databasev1beta1.DbCreateHash] = dbCreateJob.GetHash()
		r.Log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.DbCreateHash]))
	}

	// database creation finished... okay to set to completed
	instance.Status.Completed = true

	return ctrl.Result{}, nil
}

// SetupWithManager -
func (r *MariaDBDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDBDatabase{}).
		Complete(r)
}
