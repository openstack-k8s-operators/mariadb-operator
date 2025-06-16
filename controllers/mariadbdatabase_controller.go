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

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	job "github.com/openstack-k8s-operators/lib-common/modules/common/job"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg/mariadb"
)

// MariaDBDatabaseReconciler reconciles a MariaDBDatabase object
type MariaDBDatabaseReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/status,verbs=get;list
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;delete;patch

// Reconcile reconcile mariadbdatabase API requests
func (r *MariaDBDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	log := GetLog(ctx, "MariaDBDatabase")

	var err error

	// Fetch the MariaDBDatabase instance
	instance := &databasev1beta1.MariaDBDatabase{}
	err = r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	helper, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// initialize status if Conditions is nil, but do not reset if it already
	// exists
	isNewInstance := instance.Status.Conditions == nil
	if isNewInstance {
		instance.Status.Conditions = condition.Conditions{}
	}

	// Save a copy of the condtions so that we can restore the LastTransitionTime
	// when a condition's state doesn't change.
	savedConditions := instance.Status.Conditions.DeepCopy()

	// Always patch the instance status when exiting this function so we can
	// persist any changes.
	defer func() {
		// Don't update the status, if Reconciler Panics
		if r := recover(); r != nil {
			log.Info(fmt.Sprintf("Panic during reconcile %v\n", r))
			panic(r)
		}
		condition.RestoreLastTransitionTimes(
			&instance.Status.Conditions, savedConditions)
		if instance.Status.Conditions.IsUnknown(condition.ReadyCondition) {
			instance.Status.Conditions.Set(
				instance.Status.Conditions.Mirror(condition.ReadyCondition))
		}
		if instance.Status.Conditions.IsTrue(condition.ReadyCondition) {
			// database creation finished... okay to set to completed
			instance.Status.Completed = true
		}
		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	// initialize conditions used later as Status=Unknown
	cl := condition.CreateList(
		condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
		condition.UnknownCondition(databasev1beta1.MariaDBServerReadyCondition, condition.InitReason, databasev1beta1.MariaDBServerReadyInitMessage),
		condition.UnknownCondition(databasev1beta1.MariaDBDatabaseReadyCondition, condition.InitReason, databasev1beta1.MariaDBDatabaseReadyInitMessage),
	)

	instance.Status.Conditions.Init(&cl)

	// Fetch the Galera instance from which we'll pull the credentials
	dbGalera, err := r.getDatabaseObject(ctx, instance)

	// if we are being deleted then we have to remove the finalizer from Galera and then remove it from ourselves
	if !instance.DeletionTimestamp.IsZero() {
		if err == nil { // so we have Galera to remove finalizer from
			if controllerutil.RemoveFinalizer(dbGalera, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
				err := r.Update(ctx, dbGalera)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		// all our external cleanup logic is done so we can remove our own finalizer to signal that we can be deleted.
		controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
		// we can unconditionally return here as this is basically the end of the delete sequence for MariaDBDatabase
		// so nothing else needs to be done in the reconcile.
		return ctrl.Result{}, nil
	}

	// we now know that this is not a delete case
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// as it is not a delete case we need to wait for Galera to exists before we can continue.
			return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		}

		return ctrl.Result{}, err
	}

	// here we know that Galera exists so add a finalizer to ourselves and to the db CR. Before this point there is no reason to have a finalizer on ourselves as nothing to cleanup.
	if controllerutil.AddFinalizer(dbGalera, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
		err := r.Update(ctx, dbGalera)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	if instance.DeletionTimestamp.IsZero() || isNewInstance { // this condition can be removed if you wish as it is always true at this point otherwise we would returned earlier.
		if controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
			// we need to persist this right away
			return ctrl.Result{}, nil
		}
	}

	//
	// Non-deletion (normal) flow follows
	//
	var dbSecret, dbContainerImage, serviceAccount string
	// NOTE(dciabrin) When configured to only allow TLS connections, all clients
	// accessing this DB must support client connection via TLS.
	useTLS := dbGalera.Spec.TLS.Enabled() && dbGalera.Spec.DisableNonTLSListeners

	if !dbGalera.Status.Bootstrapped {
		log.Info("DB bootstrap not complete. Requeue...")

		instance.Status.Conditions.MarkFalse(
			databasev1beta1.MariaDBServerReadyCondition,
			databasev1beta1.ReasonDBWaitingInitialized,
			condition.SeverityInfo,
			databasev1beta1.MariaDBServerNotBootstrappedMessage,
		)

		return ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	dbSecret = dbGalera.Spec.Secret
	dbContainerImage = dbGalera.Spec.ContainerImage
	serviceAccount = dbGalera.RbacResourceName()

	dbHostname, dbHostResult, err := databasev1beta1.GetServiceHostname(ctx, helper, dbGalera.Name, dbGalera.Namespace)

	if (err != nil || dbHostResult != ctrl.Result{}) {
		return dbHostResult, err
	}

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBServerReadyCondition,
		databasev1beta1.MariaDBServerReadyMessage,
	)

	// Define a new Job object (hostname, password, containerImage)
	jobDef, err := mariadb.DbDatabaseJob(instance, dbHostname, dbSecret, dbContainerImage, serviceAccount, useTLS, dbGalera.Spec.NodeSelector)
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
		log.Info("Job hash added", "Job", jobDef.Name, "Hash", instance.Status.Hash[databasev1beta1.DbCreateHash])
	}

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBDatabaseReadyCondition,
		databasev1beta1.MariaDBDatabaseReadyMessage,
	)

	// DB instances supports TLS
	instance.Status.TLSSupport = dbGalera.Spec.TLS.Enabled()

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager -
func (r *MariaDBDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDBDatabase{}).
		Complete(r)
}

// getDatabaseObject - returns a Galera object
func (r *MariaDBDatabaseReconciler) getDatabaseObject(ctx context.Context, instance *databasev1beta1.MariaDBDatabase) (*databasev1beta1.Galera, error) {
	return GetDatabaseObject(
		ctx, r.Client,
		instance.ObjectMeta.Labels["dbName"],
		instance.Namespace,
	)
}
