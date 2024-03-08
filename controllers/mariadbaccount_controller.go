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
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg/mariadb"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbaccounts/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;create;update;delete;

// Reconcile
func (r *MariaDBAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	log := GetLog(ctx, "MariaDBAccount")

	var err error

	instance := &databasev1beta1.MariaDBAccount{}
	err = r.Client.Get(ctx, req.NamespacedName, instance)
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
		// update the Ready condition based on the sub conditions
		if instance.Status.Conditions.AllSubConditionIsTrue() {
			instance.Status.Conditions.MarkTrue(
				condition.ReadyCondition, condition.ReadyMessage)
		} else {
			// something is not ready so reset the Ready condition
			instance.Status.Conditions.MarkUnknown(
				condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage)
			// and recalculate it based on the state of the rest of the conditions
			instance.Status.Conditions.Set(
				instance.Status.Conditions.Mirror(condition.ReadyCondition))
		}

		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}

		// initialize conditions used later as Status=Unknown
		cl := condition.CreateList(
			condition.UnknownCondition(databasev1beta1.MariaDBServerReadyCondition, condition.InitReason, databasev1beta1.MariaDBServerReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBDatabaseReadyCondition, condition.InitReason, databasev1beta1.MariaDBDatabaseReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBAccountReadyCondition, condition.InitReason, databasev1beta1.MariaDBAccountReadyInitMessage),
		)

		instance.Status.Conditions.Init(&cl)

		// Register overall status immediately to have an early feedback e.g. in the cli
		return ctrl.Result{}, nil
	}

	if instance.DeletionTimestamp.IsZero() {
		return r.reconcileCreate(ctx, req, log, helper, instance)
	} else {
		return r.reconcileDelete(ctx, req, log, helper, instance)
	}

}

// reconcileDelete - run reconcile for case where delete timestamp is zero
func (r *MariaDBAccountReconciler) reconcileCreate(
	ctx context.Context, req ctrl.Request, log logr.Logger,
	helper *helper.Helper, instance *databasev1beta1.MariaDBAccount) (result ctrl.Result, _err error) {

	// this is following from how the MariaDBDatabase CRD works.
	// the related Galera / MariaDB object is given as a label, while
	// the reference to the secret itself is given in the spec
	// the convention appears to be: "things we are dependent on are named in labels,
	// things we are setting up are named in the spec"
	mariadbDatabaseName := instance.ObjectMeta.Labels["mariaDBDatabaseName"]
	if mariadbDatabaseName == "" {

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' does not have a 'mariaDBDatabaseName' label, create won't proceed",
			instance.Name,
		))

		return ctrl.Result{}, nil
	}

	// locate the MariaDBDatabase object that this account is associated with
	mariadbDatabase, err := r.getMariaDBDatabaseObject(ctx, instance, mariadbDatabaseName)

	// not found
	if err != nil && k8s_errors.IsNotFound(err) {
		// for the create case, need to wait for the MariaDBDatabase to exists before we can continue;
		// requeue

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBDatabaseReadyCondition,
			databasev1beta1.ReasonDBNotFound,
			condition.SeverityInfo,
			databasev1beta1.MariaDBDatabaseReadyInitMessage))

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' didn't find MariaDBDatabase '%s'; requeueing",
			instance.Name, instance.ObjectMeta.Labels["mariaDBDatabaseName"]))

		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	} else if err == nil && !mariadbDatabase.Status.Conditions.IsTrue(databasev1beta1.MariaDBDatabaseReadyCondition) {
		// found but database not ready

		// for the create case, need to wait for the MariaDBDatabase to exists before we can continue;
		// requeue

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBDatabaseReadyCondition,
			databasev1beta1.ReasonDBWaitingInitialized,
			condition.SeverityInfo,
			databasev1beta1.MariaDBDatabaseReadyInitMessage))

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' MariaDBDatabase '%s' not yet complete; requeueing",
			instance.Name, instance.ObjectMeta.Labels["mariaDBDatabaseName"]))

		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	} else if err != nil {
		// unhandled error; exit
		log.Error(err, "unhandled error retrieving MariaDBDatabase instance")

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBDatabaseReadyCondition,
			condition.ErrorReason,
			condition.SeverityError,
			databasev1beta1.MariaDBErrorRetrievingMariaDBDatabaseMessage,
			err))

		return ctrl.Result{}, err
	}

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBDatabaseReadyCondition,
		databasev1beta1.MariaDBDatabaseReadyMessage,
	)

	// first, add a finalizer for us, so that subsequent steps can make
	// additional state changes that we'd be on the hook to clean up afterwards
	if controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		// we need to persist this right away
		return ctrl.Result{}, nil
	}

	// MariaDBdatabase exists and we are a create case.  ensure finalizers set up
	if controllerutil.AddFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
		err := r.Update(ctx, mariadbDatabase)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// now proceed to do actual work.  acquire the galera/mariadb instance
	// referenced by the MariaDBDatabase which will lead us to the hostname
	// and container image to target

	dbGalera, err := r.getDatabaseObject(ctx, mariadbDatabase, instance)
	if err != nil {

		log.Error(err, "Error getting database object")

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBServerReadyCondition,
			condition.ErrorReason,
			condition.SeverityError,
			databasev1beta1.MariaDBErrorRetrievingMariaDBGaleraMessage,
			err))

		return ctrl.Result{}, err
	}

	var dbInstance, dbAdminSecret, dbContainerImage, serviceAccountName string

	if !dbGalera.Status.Bootstrapped {
		log.Info("DB bootstrap not complete. Requeue...")
		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	dbInstance = dbGalera.Name
	dbAdminSecret = dbGalera.Spec.Secret
	dbContainerImage = dbGalera.Spec.ContainerImage
	serviceAccountName = dbGalera.RbacResourceName()

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBServerReadyCondition,
		databasev1beta1.MariaDBServerReadyMessage,
	)

	// account create

	// ensure secret is present before running a job
	_, secret_result, err := secret.VerifySecret(
		ctx,
		types.NamespacedName{Name: instance.Spec.Secret, Namespace: instance.Namespace},
		[]string{databasev1beta1.DatabasePasswordSelector},
		r.Client,
		time.Duration(30)*time.Second,
	)
	if err != nil {

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBAccountReadyCondition,
			secret.ReasonSecretMissing,
			condition.SeverityInfo,
			databasev1beta1.MariaDBAccountSecretNotReadyMessage, err))

		return secret_result, err
	}

	log.Info(fmt.Sprintf("Running account create '%s' MariaDBDatabase '%s'", instance.Name, mariadbDatabaseName))

	jobDef, err := mariadb.CreateDbAccountJob(instance, mariadbDatabase.Spec.Name, dbInstance, dbAdminSecret, dbContainerImage, serviceAccountName)
	if err != nil {
		return ctrl.Result{}, err
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
	if (ctrlResult != ctrl.Result{}) {
		// TODO: should this be ctrlResult, err ?
		return ctrlResult, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if accountCreateJob.HasChanged() {
		if instance.Status.Hash == nil {
			instance.Status.Hash = make(map[string]string)
		}
		instance.Status.Hash[databasev1beta1.AccountCreateHash] = accountCreateJob.GetHash()
		log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.AccountCreateHash]))
	}

	// database creation finished

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBAccountReadyCondition,
		databasev1beta1.MariaDBAccountReadyMessage,
	)

	return ctrl.Result{}, nil
}

// reconcileDelete - run reconcile for case where delete timestamp is non zero
func (r *MariaDBAccountReconciler) reconcileDelete(
	ctx context.Context, req ctrl.Request, log logr.Logger,
	helper *helper.Helper, instance *databasev1beta1.MariaDBAccount) (result ctrl.Result, _err error) {

	// this is following from how the MariaDBDatabase CRD works.
	// the related Galera / MariaDB object is given as a label, while
	// the reference to the secret itself is given in the spec
	// the convention appears to be: "things we are dependent on are named in labels,
	// things we are setting up are named in the spec"
	mariadbDatabaseName := instance.ObjectMeta.Labels["mariaDBDatabaseName"]
	if mariadbDatabaseName == "" {

		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' does not have a 'mariaDBDatabaseName' label, will remove finalizers",
			instance.Name,
		))
		controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

		return ctrl.Result{}, nil
	}

	// locate the MariaDBDatabase object that this account is associated with
	mariadbDatabase, err := r.getMariaDBDatabaseObject(ctx, instance, mariadbDatabaseName)

	// not found
	if err != nil && k8s_errors.IsNotFound(err) {
		// for the delete case, the database doesn't exist.  so
		// that means we don't, either.   remove finalizer from
		// our own instance and return
		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' Didn't find MariaDBDatabase '%s'; no account delete needed",
			instance.Name, instance.ObjectMeta.Labels["mariaDBDatabaseName"]))

		controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

		return ctrl.Result{}, nil
	} else if err == nil && !mariadbDatabase.Status.Conditions.IsTrue(databasev1beta1.MariaDBDatabaseReadyCondition) {
		// found but database is not ready

		// for the delete case, the database doesn't exist.  so
		// that means we don't, either.   remove finalizer from
		// our own instance and return
		log.Info(fmt.Sprintf(
			"MariaDBAccount '%s' MariaDBDatabase '%s' not yet complete; no account delete needed",
			instance.Name, instance.ObjectMeta.Labels["mariaDBDatabaseName"]))

		// first, remove finalizer from the MariaDBDatabase instance
		if controllerutil.RemoveFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
			err = r.Update(ctx, mariadbDatabase)

			if err != nil && !k8s_errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}

		}

		// then remove finalizer from our own instance
		controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

		return ctrl.Result{}, nil
	} else if err != nil {
		// unhandled error; exit
		log.Error(err, "unhandled error retrieving MariaDBDatabase instance")

		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBDatabaseReadyCondition,
			condition.ErrorReason,
			condition.SeverityError,
			"Error retrieving MariaDBDatabase instance %s",
			err))

		return ctrl.Result{}, err
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
		return ctrl.Result{}, err
	} else {
		instance.Status.Conditions.MarkTrue(
			databasev1beta1.MariaDBAccountReadyCondition,
			databasev1beta1.MariaDBAccountReadyForDeleteMessage,
		)
	}

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBDatabaseReadyCondition,
		databasev1beta1.MariaDBDatabaseReadyMessage,
	)

	// now proceed to do actual work.  acquire the galera/mariadb instance
	// referenced by the MariaDBDatabase which will lead us to the hostname
	// and container image to target

	dbGalera, err := r.getDatabaseObject(ctx, mariadbDatabase, instance)
	if err != nil {

		log.Error(err, "Error getting database object")

		if k8s_errors.IsNotFound(err) {
			// remove finalizer from the MariaDBDatabase instance
			if controllerutil.RemoveFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
				err = r.Update(ctx, mariadbDatabase)

				if err != nil && !k8s_errors.IsNotFound(err) {
					return ctrl.Result{}, err
				}

			}

			// remove local finalizer
			controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

			// galera DB does not exist, so return
			return ctrl.Result{}, nil
		} else {
			instance.Status.Conditions.Set(condition.FalseCondition(
				databasev1beta1.MariaDBServerReadyCondition,
				condition.ErrorReason,
				condition.SeverityError,
				"Error retrieving MariaDB/Galera instance %s",
				err))

			return ctrl.Result{}, err
		}
	}

	var dbInstance, dbAdminSecret, dbContainerImage, serviceAccountName string

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

	dbInstance = dbGalera.Name
	dbAdminSecret = dbGalera.Spec.Secret
	dbContainerImage = dbGalera.Spec.ContainerImage
	serviceAccountName = dbGalera.RbacResourceName()

	instance.Status.Conditions.MarkTrue(
		databasev1beta1.MariaDBServerReadyCondition,
		databasev1beta1.MariaDBServerReadyMessage,
	)

	// account delete

	log.Info(fmt.Sprintf("Running account delete '%s' MariaDBDatabase '%s'", instance.Name, mariadbDatabaseName))

	jobDef, err := mariadb.DeleteDbAccountJob(instance, mariadbDatabase.Spec.Name, dbInstance, dbAdminSecret, dbContainerImage, serviceAccountName)
	if err != nil {
		return ctrl.Result{}, err
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
	if (ctrlResult != ctrl.Result{}) {
		// TODO: should this be ctrlResult, err ?
		return ctrlResult, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if accountDeleteJob.HasChanged() {
		if instance.Status.Hash == nil {
			instance.Status.Hash = make(map[string]string)
		}
		instance.Status.Hash[databasev1beta1.AccountDeleteHash] = accountDeleteJob.GetHash()
		log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.Hash[databasev1beta1.AccountDeleteHash]))
	}

	// first, remove finalizer from the MariaDBDatabase instance
	if controllerutil.RemoveFinalizer(mariadbDatabase, fmt.Sprintf("%s-%s", helper.GetFinalizer(), instance.Name)) {
		err = r.Update(ctx, mariadbDatabase)
	}

	// then remove finalizer from our own instance
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

	return ctrl.Result{}, err
}

// getDatabaseObject - returns either a Galera or MariaDB object (and an associated client.Object interface)
func (r *MariaDBAccountReconciler) getDatabaseObject(ctx context.Context, mariaDBDatabase *databasev1beta1.MariaDBDatabase, instance *databasev1beta1.MariaDBAccount) (*databasev1beta1.Galera, error) {
	dbName := mariaDBDatabase.ObjectMeta.Labels["dbName"]
	return GetDatabaseObject(
		r.Client, ctx,
		dbName,
		instance.Namespace,
	)

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

	return mariaDBDatabase, err

}
