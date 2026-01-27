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

package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	backup "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb/backup"
)

// GaleraRestoreReconciler reconciles a GaleraRestore object
type GaleraRestoreReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

// RBAC for galerarestore resources
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerarestores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerarestores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerarestores/finalizers,verbs=update

// RBAC for galerabackup resources
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerabackups,verbs=get

// RBAC for galera resources
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras,verbs=get
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/status,verbs=get

// RBAC for pods
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

// RBAC for services and endpoints
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;update;patch

// RBAC for secrets
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get

// RBAC permissions to create service accounts, roles, role bindings
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update;patch

// RBAC required to grant the service account role these capabilities
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch

// Reconcile handles the reconciliation logic for GaleraRestore resources
func (r *GaleraRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	log := GetLog(ctx, "galerabackup")

	// Fetch the GaleraRestore instance
	instance := &mariadbv1.GaleraRestore{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
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

	//
	// initialize status
	//
	// initialize conditions used later as Status=Unknown
	cl := condition.CreateList(
		condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
		// GaleraBackup exists
		condition.UnknownCondition(mariadbv1.GaleraBackupReadyCondition, condition.InitReason, condition.ReadyInitMessage),
	)

	instance.Status.Conditions.Init(&cl)
	instance.Status.ObservedGeneration = instance.Generation

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) || isNewInstance {
		return ctrl.Result{}, err
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	//
	// Service account, role, binding
	//
	rbacRules := []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"anyuid"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{"get", "list", "update", "patch"},
		},
		{
			APIGroups: []string{"mariadb.openstack.org"},
			Resources: []string{"galeras/status"},
			Verbs:     []string{"get", "list"},
		},
		{
			APIGroups: []string{"mariadb.openstack.org"},
			Resources: []string{"galeras"},
			Verbs:     []string{"get", "list"},
		},
		{
			APIGroups: []string{"mariadb.openstack.org"},
			Resources: []string{"galerabackups"},
			Verbs:     []string{"get", "list"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get"},
		},
	}
	rbacResult, err := common_rbac.ReconcileRbac(ctx, helper, instance, rbacRules)
	if err != nil {
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		return rbacResult, nil
	}

	//
	// Check for the required GaleraBackup
	//

	galeraBackup, res, err := r.ensureGaleraBackup(ctx, helper, instance)
	if galeraBackup == nil || err != nil {
		return res, err
	}

	instance.Status.Conditions.MarkTrue(
		mariadbv1.GaleraBackupReadyCondition,
		mariadbv1.GaleraBackupReadyMessage,
	)

	galera := &mariadbv1.Galera{}
	galera.Name = galeraBackup.Spec.DatabaseInstance

	// prepare a restore Pod to access the PVC holding the backups
	// the Pod's properties are taken from the pod template configured
	// in the backup's cronjob.
	backupCronJob := &batchv1.CronJob{}
	backupCronJobName := backup.BackupCronJobName(galeraBackup, galera)
	err = r.Get(ctx, types.NamespacedName{Name: backupCronJobName, Namespace: galeraBackup.Namespace}, backupCronJob)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Wait for a GaleraBackup object to exist before creating any object
			// Since the GaleraBackup object should already exist, we treat this as a warning.
			instance.Status.Conditions.MarkFalse(
				mariadbv1.GaleraBackupReadyCondition,
				mariadbv1.GaleraBackupNotFound,
				condition.SeverityWarning,
				mariadbv1.GaleraBackupInitMessage,
				err,
			)
			return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	preparedRestorePod := backup.RestorePod(instance, galeraBackup, backupCronJob)
	restorePod := &corev1.Pod{}
	err = r.Get(ctx, types.NamespacedName{Name: preparedRestorePod.Name, Namespace: preparedRestorePod.Namespace}, restorePod)
	if err != nil && k8s_errors.IsNotFound(err) {
		restorePod.ObjectMeta = preparedRestorePod.ObjectMeta
		op, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), restorePod, func() error {
			restorePod.Spec = preparedRestorePod.Spec
			err := controllerutil.SetControllerReference(helper.GetBeforeObject(), restorePod, helper.GetScheme())
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			helper.GetLogger().Error(err, "CreateOrPatch failed", "pvc", restorePod)
			return ctrl.Result{}, err
		}
		helper.GetLogger().Info(fmt.Sprintf("GaleraRestore Pod %s - %s", restorePod.Name, op))
	}

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GaleraRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.GaleraRestore{}).
		Complete(r)
}

func (r *GaleraRestoreReconciler) reconcileDelete(ctx context.Context, instance *mariadbv1.GaleraRestore, helper *helper.Helper) (ctrl.Result, error) {
	// Remove our finalizer from the target GaleraBackup
	galeraBackup := &mariadbv1.GaleraBackup{}
	galeraBackupName := types.NamespacedName{Name: instance.Spec.BackupSource, Namespace: instance.GetNamespace()}
	err := r.Get(ctx, galeraBackupName, galeraBackup)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !k8s_errors.IsNotFound(err) && galeraBackup != nil {
		if controllerutil.RemoveFinalizer(galeraBackup, helper.GetFinalizer()) {
			err := r.Update(ctx, galeraBackup)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// It's now safe to remove our GaleraRestore finalizer.
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	helper.GetLogger().Info("Reconciled GaleraRestore delete successfully")

	return ctrl.Result{}, nil
}

//
// Utility functions
//

func (r *GaleraRestoreReconciler) ensureGaleraBackup(
	ctx context.Context,
	h *helper.Helper,
	instance *mariadbv1.GaleraRestore,
) (*mariadbv1.GaleraBackup, ctrl.Result, error) {

	galeraBackup := &mariadbv1.GaleraBackup{}
	galeraBackupName := types.NamespacedName{Name: instance.Spec.BackupSource, Namespace: instance.GetNamespace()}
	err := r.Get(ctx, galeraBackupName, galeraBackup)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			instance.Status.Conditions.MarkFalse(
				mariadbv1.GaleraBackupReadyCondition,
				mariadbv1.GaleraBackupNotFound,
				condition.SeverityInfo,
				mariadbv1.GaleraBackupNotFoundMessage,
				galeraBackupName.Name,
			)
			return nil, ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		}

		// Error reading back the object.
		instance.Status.Conditions.Set(condition.FalseCondition(
			mariadbv1.GaleraBackupReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			mariadbv1.GaleraBackupErrorMessage,
			err.Error()))
		return nil, ctrl.Result{}, err
	}

	// Add finalizer to GaleraBackup to prevent it from being deleted now that we're using it
	if controllerutil.AddFinalizer(galeraBackup, h.GetFinalizer()) {
		err := r.Update(ctx, galeraBackup)
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				mariadbv1.GaleraBackupReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				mariadbv1.GaleraBackupErrorMessage,
				err.Error()))
			return nil, ctrl.Result{}, err
		}
	}

	if !galeraBackup.IsReady() {
		instance.Status.Conditions.MarkFalse(
			mariadbv1.GaleraBackupReadyCondition,
			mariadbv1.GaleraBackupNotReady,
			condition.SeverityInfo,
			mariadbv1.GaleraBackupInitMessage,
			galeraBackupName.Name,
		)
		return nil, ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	return galeraBackup, ctrl.Result{}, err
}
