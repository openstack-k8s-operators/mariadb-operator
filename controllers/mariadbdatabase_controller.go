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
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;delete;patch

// Reconcile reconcile mariadbdatabase API requests
func (r *MariaDBDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("mariadbdatabase", req.NamespacedName)

	// Fetch the MariaDBDatabase instance
	instance := &databasev1beta1.MariaDBDatabase{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Fetch the MariaDB instance from which we'll pull the credentials
	db := &databasev1beta1.MariaDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.ObjectMeta.Labels["dbName"],
			Namespace: req.Namespace,
		},
	}
	objectKey := client.ObjectKeyFromObject(db)
	err = r.Client.Get(ctx, objectKey, db)
	if err != nil {
		if !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 10}, nil
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

	finalizerName := "mariadb-" + instance.Name
	// if deletion timestamp is set on the instance object, the CR got deleted
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// if it is a new instance, add the finalizer
		if !controllerutil.ContainsFinalizer(instance, finalizerName) {
			controllerutil.AddFinalizer(instance, finalizerName)
			err = r.Client.Update(ctx, instance)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Log.Info(fmt.Sprintf("Finalizer %s added to CR %s", finalizerName, instance.Name))
		}

	} else {
		// 1. check if finalizer is there
		// Reconcile if finalizer got already removed
		if !controllerutil.ContainsFinalizer(instance, finalizerName) {
			return ctrl.Result{}, nil
		}

		// 2. delete the database
		r.Log.Info(fmt.Sprintf("CR %s delete, running DB delete job", instance.Name))
		jobDef, err := mariadb.DeleteDbDatabaseJob(instance, db.Name, db.Spec.Secret, db.Spec.ContainerImage)
		if err != nil {
			return ctrl.Result{}, err
		}

		dbDeleteHash := instance.Status.Hash[databasev1beta1.DbDeleteHash]
		dbDeleteJob := job.NewJob(
			jobDef,
			"deleteDB_"+instance.Name,
			false,
			5,
			dbDeleteHash,
		)
		ctrlResult, err := dbDeleteJob.DoJob(
			ctx,
			helper,
		)
		if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		if dbDeleteJob.HasChanged() {
			if instance.Status.Hash == nil {
				instance.Status.Hash = make(map[string]string)
			}
			instance.Status.Hash[databasev1beta1.DbDeleteHash] = dbDeleteJob.GetHash()
			if err := r.Client.Status().Update(ctx, instance); err != nil {
				return ctrl.Result{}, err
			}
			r.Log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.DbDeleteHash]))
		}

		// 3. as last step remove the finalizer on the operator CR to finish delete
		controllerutil.RemoveFinalizer(instance, finalizerName)
		err = r.Client.Update(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info(fmt.Sprintf("CR %s deleted", instance.Name))
		return ctrl.Result{}, nil
	}

	if db.Status.DbInitHash == "" {
		r.Log.Info("DB initialization not complete. Requeue...")
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	// Define a new Job object (hostname, password, containerImage)
	jobDef, err := mariadb.DbDatabaseJob(instance, db.Name, db.Spec.Secret, db.Spec.ContainerImage)
	if err != nil {
		return ctrl.Result{}, err
	}
	dbCreateHash := instance.Status.Hash[databasev1beta1.DbCreateHash]
	dbCreateJob := job.NewJob(
		jobDef,
		databasev1beta1.DbCreateHash,
		false,
		5,
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
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.DbCreateHash]))
	}

	// database creation finished... okay to set to completed
	if err := r.setCompleted(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *MariaDBDatabaseReconciler) setCompleted(ctx context.Context, db *databasev1beta1.MariaDBDatabase) error {

	if !db.Status.Completed {
		db.Status.Completed = true
		if err := r.Client.Status().Update(ctx, db); err != nil {
			return err
		}
	}
	return nil
}

// SetupWithManager -
func (r *MariaDBDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDBDatabase{}).
		Complete(r)
}
