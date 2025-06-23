/*
Copyright 2022.

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
	"strings"
	"time"

	"github.com/go-logr/logr"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	job "github.com/openstack-k8s-operators/lib-common/modules/common/job"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg/mariadb"
	batchv1 "k8s.io/api/batch/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// MariaDBAccountReconciler reconciles a MariaDBAccount object
type MariaDBAccountReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// SetupWithManager -
func (r *MariaDBAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDBAccount{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbaccounts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbaccounts/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;create;update;delete;patch

// Reconcile
func (r *MariaDBAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	log := GetLog(ctx, "MariaDBAccount")

	instance := &databasev1beta1.MariaDBAccount{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
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
		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	var cl condition.Conditions
	if instance.IsUserAccount() {
		// initialize conditions used later as Status=Unknown
		cl = condition.CreateList(
			condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBServerReadyCondition, condition.InitReason, databasev1beta1.MariaDBServerReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBDatabaseReadyCondition, condition.InitReason, databasev1beta1.MariaDBDatabaseReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBAccountReadyCondition, condition.InitReason, databasev1beta1.MariaDBAccountReadyInitMessage),
		)
	} else {
		// initialize conditions used later as Status=Unknown
		cl = condition.CreateList(
			condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBServerReadyCondition, condition.InitReason, databasev1beta1.MariaDBServerReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBAccountReadyCondition, condition.InitReason, databasev1beta1.MariaDBAccountReadyInitMessage),
		)
	}
	instance.Status.Conditions.Init(&cl)

	if instance.DeletionTimestamp.IsZero() || isNewInstance { //revive:disable:indent-error-flow
		return r.reconcileCreateOrUpdate(ctx, log, helper, instance)
	} else {
		return r.reconcileDelete(ctx, log, helper, instance)
	}

}

// reconcileCreateOrUpdate - run reconcile for case where delete timestamp is zero
func (r *MariaDBAccountReconciler) reconcileCreateOrUpdate(
	ctx context.Context, log logr.Logger,
	helper *helper.Helper, instance *databasev1beta1.MariaDBAccount) (result ctrl.Result, _err error) {

	var mariadbDatabase *databasev1beta1.MariaDBDatabase
	var err error

	log.Info("Reconcile MariaDBAccount create or update")

	if instance.IsUserAccount() {
		// for User account, get a handle to the current, active MariaDBDatabase.
		// if not ready yet, requeue.
		mariadbDatabase, result, err = r.getMariaDBDatabaseForCreate(ctx, log, instance)
		if mariadbDatabase == nil {
			return result, err
		}
	}

	if controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		// we need to persist this right away
		return ctrl.Result{}, nil
	}

	if instance.IsUserAccount() {
		// MariaDBdatabase exists and we are a create case.  ensure finalizers set up
		if controllerutil.AddFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
			err = r.Update(ctx, mariadbDatabase)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// now proceed to do actual work.  acquire the Galera instance
	// which will lead us to the hostname and container image to target
	dbGalera, dbHostname, result, err := r.getGaleraForCreateOrDelete(
		ctx, log, helper, instance, mariadbDatabase,
	)
	if dbGalera == nil {
		return result, err
	}

	dbContainerImage := dbGalera.Spec.ContainerImage
	serviceAccountName := dbGalera.RbacResourceName()

	// account create

	// ensure secret is present, add a finalizer for mariadbaccount
	result, err = r.ensureAccountSecret(ctx, log, helper, instance)
	if (result != ctrl.Result{} || err != nil) {
		return result, err
	}

	var jobDef *batchv1.Job

	var createOrUpdate string
	if instance.Status.CurrentSecret != "" {
		createOrUpdate = "update"
	} else {
		createOrUpdate = "create"
	}

	if instance.IsUserAccount() {
		log.Info(fmt.Sprintf("Checking %s account job '%s' MariaDBDatabase '%s'",
			createOrUpdate,
			instance.Name, mariadbDatabase.Spec.Name))
		jobDef, err = mariadb.CreateOrUpdateDbAccountJob(
			dbGalera, instance, mariadbDatabase.Spec.Name, dbHostname,
			dbContainerImage, serviceAccountName, dbGalera.Spec.NodeSelector)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		log.Info(fmt.Sprintf("Checking %s system account job '%s'", createOrUpdate,
			instance.Name))
		jobDef, err = mariadb.CreateOrUpdateDbAccountJob(
			dbGalera, instance, "", dbHostname,
			dbContainerImage, serviceAccountName, dbGalera.Spec.NodeSelector)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	accountCreateHash := instance.Status.Hash[databasev1beta1.AccountCreateHash]
	accountCreateJob := job.NewJob(
		jobDef,
		databasev1beta1.AccountCreateHash,
		false,
		time.Duration(5)*time.Second,
		accountCreateHash,
	)
	ctrlResult, err := accountCreateJob.DoJob(
		ctx,
		helper,
	)
	if (ctrlResult != ctrl.Result{} || err != nil) {
		return ctrlResult, err
	}

	if accountCreateJob.HasChanged() {

		if instance.Status.Hash == nil {
			instance.Status.Hash = make(map[string]string)
		}
		instance.Status.Hash[databasev1beta1.AccountCreateHash] = accountCreateJob.GetHash()
		log.Info(fmt.Sprintf("Job %s hash created or updated - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.AccountCreateHash]))

		// set up new Secret and remove finalizer from old secret
		if instance.Status.CurrentSecret != instance.Spec.Secret {
			currentSecret := instance.Status.CurrentSecret
			err = r.removeSecretFinalizer(ctx, log, helper, currentSecret, instance.Namespace)
			if err == nil {
				instance.Status.CurrentSecret = instance.Spec.Secret
			} else {
				return ctrl.Result{}, err
			}
		}

	}

	// database creation finished

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBAccountReadyCondition,
		databasev1beta1.MariaDBAccountReadyMessage,
	)

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}
	return ctrl.Result{}, nil
}

// reconcileDelete - run reconcile for case where delete timestamp is non zero
func (r *MariaDBAccountReconciler) reconcileDelete(
	ctx context.Context, log logr.Logger,
	helper *helper.Helper, instance *databasev1beta1.MariaDBAccount) (result ctrl.Result, _err error) {

	var mariadbDatabase *databasev1beta1.MariaDBDatabase
	var err error

	log.Info("Reconcile MariaDBAccount delete")

	if instance.IsUserAccount() {
		mariadbDatabase, result, err = r.getMariaDBDatabaseForDelete(ctx, log, helper, instance)
		if mariadbDatabase == nil {
			return result, err
		}
	}

	// dont do actual DROP USER until finalizers from downstream controllers
	// are removed from the CR.
	finalizers := instance.GetFinalizers()
	finalizersWeCareAbout := []string{}

	for _, f := range finalizers {
		if f != helper.GetFinalizer() {
			finalizersWeCareAbout = append(finalizersWeCareAbout, f)
		}
	}

	if len(finalizersWeCareAbout) > 0 {
		instance.Status.Conditions.MarkFalse(
			databasev1beta1.MariaDBAccountReadyCondition,
			condition.DeletingReason,
			condition.SeverityInfo,
			databasev1beta1.MariaDBAccountFinalizersRemainMessage,
			strings.Join(finalizersWeCareAbout, ", "),
		)
		return ctrl.Result{}, nil
	}

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBAccountReadyCondition,
		databasev1beta1.MariaDBAccountReadyForDeleteMessage,
	)

	if instance.IsUserAccount() {
		instance.Status.Conditions.MarkTrue(
			databasev1beta1.MariaDBDatabaseReadyCondition,
			databasev1beta1.MariaDBDatabaseReadyMessage,
		)
	}

	// now proceed to do actual work.  acquire the Galera instance
	// which will lead us to the hostname and container image to target
	dbGalera, dbHostname, result, err := r.getGaleraForCreateOrDelete(
		ctx, log, helper, instance, mariadbDatabase,
	)

	// if Galera CR was not found or in a deletion process, this indicates
	// we won't ever have a DB server with which to run a DROP for the
	// account, so remove all finalizers and exit

	var galeraGone bool

	if k8s_errors.IsNotFound(err) {
		log.Info("Galera instance not found, so we will remove finalizers and skip account delete")
		galeraGone = true
	} else if err != nil {
		// unexpected error code
		return result, err
	} else if dbGalera == nil || !dbGalera.DeletionTimestamp.IsZero() {
		log.Info("Galera deleted or deletion timestamp is non-zero, so we will remove finalizers and skip account delete")
		galeraGone = true
	} else {
		galeraGone = false
	}

	if galeraGone {
		if instance.IsUserAccount() {
			// remove finalizer from the MariaDBDatabase instance
			if controllerutil.RemoveFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
				err = r.Update(ctx, mariadbDatabase)

				if err != nil && !k8s_errors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
			}
		}

		// remove local finalizer
		err := r.removeAccountAndSecretFinalizer(ctx, log, helper, instance)
		return ctrl.Result{}, err
	}

	dbContainerImage := dbGalera.Spec.ContainerImage
	serviceAccountName := dbGalera.RbacResourceName()

	var jobDef *batchv1.Job

	if instance.IsUserAccount() {

		log.Info(fmt.Sprintf("Running account delete '%s' MariaDBDatabase '%s'", instance.Name, mariadbDatabase.Spec.Name))

		jobDef, err = mariadb.DeleteDbAccountJob(dbGalera, instance, mariadbDatabase.Spec.Name, dbHostname, dbContainerImage, serviceAccountName, dbGalera.Spec.NodeSelector)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		log.Info(fmt.Sprintf("Running system account delete '%s'", instance.Name))

		jobDef, err = mariadb.DeleteDbAccountJob(dbGalera, instance, "", dbHostname, dbContainerImage, serviceAccountName, dbGalera.Spec.NodeSelector)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	accountDeleteHash := instance.Status.Hash[databasev1beta1.AccountDeleteHash]
	accountDeleteJob := job.NewJob(
		jobDef,
		databasev1beta1.AccountDeleteHash,
		false,
		time.Duration(5)*time.Second,
		accountDeleteHash,
	)
	ctrlResult, err := accountDeleteJob.DoJob(
		ctx,
		helper,
	)
	if (ctrlResult != ctrl.Result{} || err != nil) {
		return ctrlResult, err
	}
	if accountDeleteJob.HasChanged() {
		if instance.Status.Hash == nil {
			instance.Status.Hash = make(map[string]string)
		}
		instance.Status.Hash[databasev1beta1.AccountDeleteHash] = accountDeleteJob.GetHash()
		log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.AccountDeleteHash]))
	}

	// first, remove finalizer from the MariaDBDatabase instance
	if instance.IsUserAccount() {
		if controllerutil.RemoveFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
			err = r.Update(ctx, mariadbDatabase)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// then remove finalizer from our own instance
	err = r.removeAccountAndSecretFinalizer(ctx, log, helper, instance)
	return ctrl.Result{}, err
}

// getMariaDBDatabaseForCreate - waits for a MariaDBDatabase to be available in preparation
// to create an account
func (r *MariaDBAccountReconciler) getMariaDBDatabaseForCreate(ctx context.Context, log logr.Logger,
	instance *databasev1beta1.MariaDBAccount) (*databasev1beta1.MariaDBDatabase, ctrl.Result, error) {

	// the convention of using a label for "things we are dependent on" is
	// taken from the same practice in MariaDBDatabase where the "dbName" label
	// refers to the Galera instance.
	mariadbDatabaseName := instance.ObjectMeta.Labels["mariaDBDatabaseName"]
	if mariadbDatabaseName == "" {

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' does not have a 'mariaDBDatabaseName' label, create won't proceed",
			instance.Name,
		))

		return nil, ctrl.Result{}, nil
	}

	// locate the MariaDBDatabase object itself
	mariadbDatabase, err := r.getMariaDBDatabaseObject(ctx, instance, mariadbDatabaseName)

	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// doesnt exist yet; requeue

			instance.Status.Conditions.Set(condition.FalseCondition(
				databasev1beta1.MariaDBDatabaseReadyCondition,
				databasev1beta1.ReasonDBNotFound,
				condition.SeverityInfo,
				databasev1beta1.MariaDBDatabaseReadyInitMessage))

			log.Info(fmt.Sprintf(
				"MariaDBAccount '%s' didn't find MariaDBDatabase '%s'; requeueing",
				instance.Name, mariadbDatabaseName))

			return nil, ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		} else {
			// unhandled error; exit without requeue
			log.Error(err, "unhandled error retrieving MariaDBDatabase instance")

			instance.Status.Conditions.Set(condition.FalseCondition(
				databasev1beta1.MariaDBDatabaseReadyCondition,
				condition.ErrorReason,
				condition.SeverityError,
				databasev1beta1.MariaDBErrorRetrievingMariaDBDatabaseMessage,
				err))

			return nil, ctrl.Result{}, err
		}
	} else if mariadbDatabase.Status.Conditions.IsFalse(databasev1beta1.MariaDBDatabaseReadyCondition) {
		// found but not ready; requeue

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBDatabaseReadyCondition,
			databasev1beta1.ReasonDBWaitingInitialized,
			condition.SeverityInfo,
			databasev1beta1.MariaDBDatabaseReadyInitMessage))

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' MariaDBDatabase '%s' not yet complete; requeueing",
			instance.Name, mariadbDatabaseName))

		return nil, ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	// MariaDBDabase is ready, update status
	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBDatabaseReadyCondition,
		databasev1beta1.MariaDBDatabaseReadyMessage,
	)

	// return MariaDBDatabase where account create flow will then continue
	return mariadbDatabase, ctrl.Result{}, nil
}

// getMariaDBDatabaseForDelete - retrieves a MariaDBDatabase to be available in preparation
// to delete an account.  if MariaDBDatabase not available, deleted, or not set up,
// removes account-level finalizers and returns nil for the object
func (r *MariaDBAccountReconciler) getMariaDBDatabaseForDelete(ctx context.Context, log logr.Logger,
	helper *helper.Helper, instance *databasev1beta1.MariaDBAccount) (*databasev1beta1.MariaDBDatabase, ctrl.Result, error) {

	mariadbDatabaseName := instance.ObjectMeta.Labels["mariaDBDatabaseName"]
	if mariadbDatabaseName == "" {

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' does not have a 'mariaDBDatabaseName' label, will remove finalizers",
			instance.Name,
		))

		// remove local finalizer
		err := r.removeAccountAndSecretFinalizer(ctx, log, helper, instance)
		return nil, ctrl.Result{}, err
	}

	// locate the MariaDBDatabase object itself
	mariadbDatabase, err := r.getMariaDBDatabaseObject(ctx, instance, mariadbDatabaseName)

	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// not found.   this implies MariaDBAccount has no database-level
			// entry either.   Remove MariaDBAccount / secret finalizers and return
			log.Info(fmt.Sprintf(
				"MariaDBAccount '%s' Didn't find MariaDBDatabase '%s'; no account delete needed",
				instance.Name, mariadbDatabaseName))

			// remove local finalizer
			err = r.removeAccountAndSecretFinalizer(ctx, log, helper, instance)
			return nil, ctrl.Result{}, err
		} else {
			// unhandled error; exit without change
			log.Error(err, "unhandled error retrieving MariaDBDatabase instance")

			instance.Status.Conditions.Set(condition.FalseCondition(
				databasev1beta1.MariaDBDatabaseReadyCondition,
				condition.ErrorReason,
				condition.SeverityError,
				databasev1beta1.MariaDBErrorRetrievingMariaDBDatabaseMessage,
				err))

			return nil, ctrl.Result{}, err

		}
	} else if mariadbDatabase.Status.Conditions.IsFalse(databasev1beta1.MariaDBDatabaseReadyCondition) {
		// found but database is not ready.   this implies MariaDBAccount has no database-level
		// entry either.   Remove MariaDBAccount / secret finalizers and return
		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' MariaDBDatabase '%s' not yet complete; no account delete needed",
			instance.Name, mariadbDatabaseName))

		if controllerutil.RemoveFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
			err = r.Update(ctx, mariadbDatabase)

			if err != nil && !k8s_errors.IsNotFound(err) {
				return nil, ctrl.Result{}, err
			}
		}

		// remove local finalizer
		err = r.removeAccountAndSecretFinalizer(ctx, log, helper, instance)
		return nil, ctrl.Result{}, err
	}

	// return MariaDBDatabase where account delete flow will then continue
	return mariadbDatabase, ctrl.Result{}, nil
}

// getGaleraForCreateOrDelete - retrieves the Galera instance in use, and establishes
// that it's in a ready state.  Sets appropriate statuses and returns requeue
// or error results as needed.
func (r *MariaDBAccountReconciler) getGaleraForCreateOrDelete(
	ctx context.Context, log logr.Logger,
	helper *helper.Helper, instance *databasev1beta1.MariaDBAccount,
	mariadbDatabase *databasev1beta1.MariaDBDatabase) (*databasev1beta1.Galera, string, ctrl.Result, error) {

	var dbGalera *databasev1beta1.Galera
	var err error
	var dbName string

	if instance.IsUserAccount() {
		dbName = mariadbDatabase.ObjectMeta.Labels["dbName"]
	} else {
		// note mariadbDatabase is passed as nil in this case
		dbName = instance.ObjectMeta.Labels["dbName"]
	}
	dbGalera, err = GetDatabaseObject(ctx, r.Client, dbName, instance.Namespace)

	if err != nil {
		if k8s_errors.IsNotFound(err) {
			log.Info(fmt.Sprintf("Galera instance '%s' does not exist", dbName))
		} else {
			log.Error(err, "Error retrieving Galera instance")
		}

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBServerReadyCondition,
			condition.ErrorReason,
			condition.SeverityError,
			databasev1beta1.MariaDBErrorRetrievingMariaDBGaleraMessage,
			err))

		return nil, "", ctrl.Result{}, err
	}

	if !dbGalera.Status.Bootstrapped {
		log.Info("DB bootstrap not complete. Requeue...")

		instance.Status.Conditions.MarkFalse(
			databasev1beta1.MariaDBServerReadyCondition,
			databasev1beta1.ReasonDBWaitingInitialized,
			condition.SeverityInfo,
			databasev1beta1.MariaDBServerNotBootstrappedMessage,
		)

		return nil, "", ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	if !dbGalera.DeletionTimestamp.IsZero() {
		log.Info("DB server marked for deletion, preventing account operations from proceeding.  Will seek to remove finalizers and exit")

		instance.Status.Conditions.MarkFalse(
			databasev1beta1.MariaDBServerReadyCondition,
			databasev1beta1.ReasonDBResourceDeleted,
			condition.SeverityInfo,
			databasev1beta1.MariaDBServerDeletedMessage,
		)

		return dbGalera, "", ctrl.Result{}, nil
	}

	dbHostname, dbHostResult, err := databasev1beta1.GetServiceHostname(ctx, helper, dbGalera.Name, dbGalera.Namespace)

	if (err != nil || dbHostResult != ctrl.Result{}) {
		return nil, "", dbHostResult, err
	}

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBServerReadyCondition,
		databasev1beta1.MariaDBServerReadyMessage,
	)

	return dbGalera, dbHostname, ctrl.Result{}, nil
}

// getMariaDBDatabaseObject - returns a MariaDBDatabase object
func (r *MariaDBAccountReconciler) getMariaDBDatabaseObject(ctx context.Context, instance *databasev1beta1.MariaDBAccount, mariadbDatabaseName string) (*databasev1beta1.MariaDBDatabase, error) {

	mariaDBDatabase := &databasev1beta1.MariaDBDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariadbDatabaseName,
			Namespace: instance.Namespace,
		},
	}

	objectKey := client.ObjectKeyFromObject(mariaDBDatabase)

	err := r.Client.Get(ctx, objectKey, mariaDBDatabase)
	if err != nil {
		return nil, err
	}

	return mariaDBDatabase, nil

}

// ensureAccountSecret - ensures the Secret exists, is valid, adds a finalizer.
// includes requeue for secret does not exist
func (r *MariaDBAccountReconciler) ensureAccountSecret(
	ctx context.Context,
	log logr.Logger,
	h *helper.Helper,
	instance *databasev1beta1.MariaDBAccount,
) (ctrl.Result, error) {

	secretName := instance.Spec.Secret
	secretNamespace := instance.Namespace
	secretObj, _, err := secret.GetSecret(ctx, h, secretName, secretNamespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			instance.Status.Conditions.Set(condition.FalseCondition(
				databasev1beta1.MariaDBAccountReadyCondition,
				secret.ReasonSecretMissing,
				condition.SeverityInfo,
				databasev1beta1.MariaDBAccountSecretNotReadyMessage, err))

			log.Info(fmt.Sprintf(
				"MariaDBAccount '%s' didn't find Secret '%s'; requeueing",
				instance.Name, instance.Spec.Secret))

			return ctrl.Result{RequeueAfter: time.Duration(30) * time.Second}, nil

		} else {
			return ctrl.Result{}, err
		}
	}

	var expectedFields = []string{databasev1beta1.DatabasePasswordSelector}

	// collect the secret values the caller expects to exist
	for _, field := range expectedFields {
		_, ok := secretObj.Data[field]
		if !ok {
			err := fmt.Errorf("%w: field %s not found in Secret %s", util.ErrFieldNotFound, field, secretName)
			return ctrl.Result{}, err
		}
	}
	if controllerutil.AddFinalizer(secretObj, h.GetFinalizer()) {
		err = r.Update(ctx, secretObj)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, err
}

// removeAccountAndSecretFinalizer - removes finalizer from mariadbaccount as well
// as current primary secret
func (r *MariaDBAccountReconciler) removeAccountAndSecretFinalizer(ctx context.Context,
	log logr.Logger, helper *helper.Helper, instance *databasev1beta1.MariaDBAccount) error {

	err := r.removeSecretFinalizer(
		ctx, log, helper, instance.Spec.Secret, instance.Namespace,
	)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return err
	}

	// remove mariadbaccount finalizer which will update at end of reconcile
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

	return nil
}

func (r *MariaDBAccountReconciler) removeSecretFinalizer(ctx context.Context,
	log logr.Logger, helper *helper.Helper, secretName string, namespace string) error {
	accountSecret, _, err := secret.GetSecret(ctx, helper, secretName, namespace)

	if err == nil {
		if controllerutil.RemoveFinalizer(accountSecret, helper.GetFinalizer()) {
			err = r.Update(ctx, accountSecret)
			if err != nil {
				log.Error(
					err,
					fmt.Sprintf("Error removing mariadbaccount finalizer from secret '%s', will try again", secretName))
				return err
			} else {
				log.Info(fmt.Sprintf("Successfully removed mariadbaccount finalizer from secret '%s'", secretName))
			}
		}
	} else if !k8s_errors.IsNotFound(err) {
		return err
	}

	return nil

}
