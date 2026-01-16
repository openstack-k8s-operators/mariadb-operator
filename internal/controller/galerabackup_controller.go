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

	"github.com/openstack-k8s-operators/lib-common/modules/common"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	configmap "github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	env "github.com/openstack-k8s-operators/lib-common/modules/common/env"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	backup "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb/backup"
)

// GaleraBackupReconciler reconciles a GaleraBackup object
type GaleraBackupReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

// RBAC for galerabackup resources
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerabackups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerabackups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=mariadb.openstack.org,resources=galerabackups/finalizers,verbs=update

// RBAC for pods
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

// RBAC for configmaps
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;

// RBAC permissions to create service accounts, roles, role bindings
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update;patch

// RBAC required to grant the service account role these capabilities
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch

// RBAC for PVC
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete;patch

// RBAC for Cronjobs
// +kubebuilder:rbac:groups="",resources=cronjobs,verbs=get;list;watch;create;update;delete;patch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;delete;patch

// Reconcile handles the reconciliation logic for GaleraBackup resources
func (r *GaleraBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	log := GetLog(ctx, "galerabackup")

	// Fetch the GaleraBackup instance
	instance := &mariadbv1.GaleraBackup{}
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
		// configmap generation
		condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
		// Galera DB exists
		condition.UnknownCondition(mariadbv1.MariaDBResourceExistsCondition, condition.InitReason, mariadbv1.MariaDBResourceInitMessage),
		// PVC objects
		condition.UnknownCondition(mariadbv1.PersistentVolumeClaimReadyCondition, condition.InitReason, mariadbv1.PersistentVolumeClaimReadyInitMessage),
		// Cronjob object
		condition.UnknownCondition(mariadbv1.CronjobReadyCondition, condition.InitReason, mariadbv1.CronjobReadyInitMessage),
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
			Verbs:     []string{"get"},
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
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"cronjobs"},
			Verbs:     []string{"get"},
		},
	}
	rbacResult, err := common_rbac.ReconcileRbac(ctx, helper, instance, rbacRules)
	if err != nil {
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		return rbacResult, nil
	}

	// Map of all resources that may cause a rolling service restart
	inputHashEnv := make(map[string]env.Setter)

	// Generate and hash config maps
	err = r.generateConfigMaps(ctx, helper, instance, &inputHashEnv)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %w", err)
	}
	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	hashOfHashes, err := util.HashOfInputHashes(inputHashEnv)
	if err != nil {
		return ctrl.Result{}, err
	}

	if hashMap, changed := util.SetHash(instance.Status.Hash, common.InputHashName, hashOfHashes); changed {
		// Hash changed and instance status should be updated (which will be done by main defer func),
		// so update all the input hashes and return to reconcile again
		instance.Status.Hash = hashMap
		for k, s := range inputHashEnv {
			var envVar corev1.EnvVar
			s(&envVar)
			instance.Status.Hash[k] = envVar.Value
		}
		helper.GetLogger().Info("Input hash changed", "hash", hashOfHashes)
		return ctrl.Result{}, nil
	}

	galera := &mariadbv1.Galera{}
	galeraName := types.NamespacedName{Name: instance.Spec.DatabaseInstance, Namespace: instance.GetNamespace()}
	err = r.Get(ctx, galeraName, galera)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Wait for a Galera object to exist before creating any object (cronjob, PVs...)
			// Since the Galera object should already exist, we treat this as a warning.
			instance.Status.Conditions.MarkFalse(
				mariadbv1.MariaDBResourceExistsCondition,
				mariadbv1.ReasonResourceNotFound,
				condition.SeverityWarning,
				mariadbv1.MariaDBResourceInitMessage,
			)
			return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	instance.Status.Conditions.MarkTrue(
		mariadbv1.MariaDBResourceExistsCondition,
		mariadbv1.MariaDBResourceExistsMessage,
	)

	pvc := &corev1.PersistentVolumeClaim{}
	backupPVC, transferPVC := backup.BackupPVCs(instance, galera)

	err = r.Get(ctx, types.NamespacedName{Name: backupPVC.Name, Namespace: backupPVC.Namespace}, backupPVC)
	if err != nil && k8s_errors.IsNotFound(err) {
		pvc.ObjectMeta = backupPVC.ObjectMeta
		op, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), pvc, func() error {
			pvc.Spec = backupPVC.Spec
			// We explicitely do not own this PVC so that backups are not lost
			// if the GaleraBackup CR is removed
			return nil
		})
		if err != nil {
			helper.GetLogger().Error(err, "CreateOrPatch failed", "pvc", pvc)
			return ctrl.Result{}, err
		}
		helper.GetLogger().Info(fmt.Sprintf("Backup PVC %s - %s", pvc.Name, op))
	}

	if instance.Spec.TransferStorage != nil {
		err = r.Get(ctx, types.NamespacedName{Name: transferPVC.Name, Namespace: transferPVC.Namespace}, transferPVC)
		if err != nil && k8s_errors.IsNotFound(err) {
			pvc.ObjectMeta = transferPVC.ObjectMeta
			op, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), pvc, func() error {
				pvc.Spec = transferPVC.Spec
				err := controllerutil.SetControllerReference(helper.GetBeforeObject(), pvc, helper.GetScheme())
				if err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				helper.GetLogger().Error(err, "CreateOrPatch failed", "pvc", pvc)
				return ctrl.Result{}, err
			}
			helper.GetLogger().Info(fmt.Sprintf("Backup transfer PVC %s - %s", pvc.Name, op))
		}
	}

	instance.Status.Conditions.MarkTrue(
		mariadbv1.PersistentVolumeClaimReadyCondition,
		mariadbv1.PersistentVolumeClaimReadyMessage,
	)

	preparedBackupCronJob := backup.BackupCronJob(instance, galera, hashOfHashes)
	backupCronJob := &batchv1.CronJob{}
	backupCronJob.ObjectMeta = preparedBackupCronJob.ObjectMeta

	err = r.Get(ctx, req.NamespacedName, backupCronJob)
	if err != nil && k8s_errors.IsNotFound(err) {
		op, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), backupCronJob, func() error {
			backupCronJob.Spec = preparedBackupCronJob.Spec
			err := controllerutil.SetControllerReference(helper.GetBeforeObject(), backupCronJob, helper.GetScheme())
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			helper.GetLogger().Error(err, "CreateOrPatch failed", "pod", backupCronJob.Name)
			instance.Status.Conditions.MarkFalse(
				mariadbv1.CronjobReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				mariadbv1.CronjobReadyErrorMessage,
				err,
			)
			return ctrl.Result{}, err
		}
		if op != controllerutil.OperationResultNone {
			helper.GetLogger().Info(fmt.Sprintf("Backup Cronjob %s - %s", backupCronJob.Name, op))
		}
	}

	instance.Status.Conditions.MarkTrue(
		mariadbv1.CronjobReadyCondition,
		mariadbv1.CronjobReadyMessage,
	)

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}

	return ctrl.Result{}, nil
}

// generateConfigMaps returns the config map resource for a galera instance
func (r *GaleraBackupReconciler) generateConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *mariadbv1.GaleraBackup,
	envVars *map[string]env.Setter,
) error {
	log := GetLog(ctx, "galera")
	cfgScripts := fmt.Sprintf("%s-backup-scripts", instance.Name)
	cfgConfig := fmt.Sprintf("%s-backup-config", instance.Name)
	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:         cfgScripts,
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeScripts,
			InstanceType: instance.Kind,
			Labels:       map[string]string{},
		},
		// ConfigMap
		{
			Name:         cfgConfig,
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeConfig,
			InstanceType: instance.Kind,
			Labels:       map[string]string{},
		},
	}

	err := configmap.EnsureConfigMaps(ctx, h, instance, cms, envVars)
	if err != nil {
		log.Error(err, "Unable to retrieve or create config maps")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GaleraBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.GaleraBackup{}).
		Complete(r)
}

func (r *GaleraBackupReconciler) reconcileDelete(_ context.Context, instance *mariadbv1.GaleraBackup, helper *helper.Helper) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	helper.GetLogger().Info("Reconciled Galera backup delete successfully")

	return ctrl.Result{}, nil
}
