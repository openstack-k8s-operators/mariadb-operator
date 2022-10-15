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
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	configmap "github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	env "github.com/openstack-k8s-operators/lib-common/modules/common/env"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	job "github.com/openstack-k8s-operators/lib-common/modules/common/job"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg"
	"k8s.io/client-go/kubernetes"
)

// GetClient -
func (r *MariaDBReconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *MariaDBReconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetLogger -
func (r *MariaDBReconciler) GetLogger() logr.Logger {
	return r.Log
}

// GetScheme -
func (r *MariaDBReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// MariaDBReconciler reconciles a MariaDB object
type MariaDBReconciler struct {
	Client  client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbs/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete;
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;delete;

// Reconcile reconcile mariadb API requests
func (r *MariaDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("mariadb", req.NamespacedName)

	// Fetch the MariaDB instance
	instance := &databasev1beta1.MariaDB{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	//
	// initialize status
	//
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}
		// initialize conditions used later as Status=Unknown
		cl := condition.CreateList(
			condition.UnknownCondition(condition.ExposeServiceReadyCondition, condition.InitReason, condition.ExposeServiceReadyInitMessage),
			condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
			condition.UnknownCondition(databasev1beta1.MariaDBInitializedCondition, condition.InitReason, databasev1beta1.MariaDBInitializedInitMessage),
			condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
		)

		instance.Status.Conditions.Init(&cl)

		// Register overall status immediately to have an early feedback e.g. in the cli
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	h, err := helper.NewHelper(
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
		// update the overall status condition if service is ready
		if instance.IsReady() {
			instance.Status.Conditions.MarkTrue(condition.ReadyCondition, condition.ReadyMessage)
		}

		if err := h.SetAfter(instance); err != nil {
			util.LogErrorForObject(h, err, "Set after and calc patch/diff", instance)
		}

		if changed := h.GetChanges()["status"]; changed {
			patch := client.MergeFrom(h.GetBeforeObject())

			if err := r.Client.Status().Patch(ctx, instance, patch); err != nil && !k8s_errors.IsNotFound(err) {
				util.LogErrorForObject(h, err, "Update status", instance)
			}
		}
	}()

	// PVC
	// TODO: Add PVC condition handling?  We don't currently in other operators that have PVC concerns, though
	pvc := mariadb.Pvc(instance)
	op, err := controllerutil.CreateOrPatch(ctx, r.Client, pvc, func() error {

		pvc.Spec.Resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse(instance.Spec.StorageRequest),
		}

		pvc.Spec.StorageClassName = &instance.Spec.StorageClass
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}

		err := controllerutil.SetOwnerReference(instance, pvc, r.Client.Scheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("%s %s database PVC %s - operation: %s", instance.Kind, instance.Name, pvc.Name, string(op)))
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	// Service
	service := mariadb.Service(instance)
	op, err = controllerutil.CreateOrPatch(ctx, r.Client, service, func() error {
		err := controllerutil.SetControllerReference(instance, service, r.Scheme)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ExposeServiceReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		util.LogForObject(
			h,
			fmt.Sprintf("Service %s successfully reconciled - operation: %s", service.Name, string(op)),
			instance,
		)
	}

	// Endpoints
	endpoints := mariadb.Endpoints(instance)
	if endpoints != nil {
		op, err = controllerutil.CreateOrPatch(ctx, r.Client, endpoints, func() error {
			err := controllerutil.SetControllerReference(instance, endpoints, r.Scheme)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.ExposeServiceReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.ExposeServiceReadyErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		if op != controllerutil.OperationResultNone {
			util.LogForObject(
				h,
				fmt.Sprintf("Endpoints %s successfully reconciled - operation: %s", endpoints.Name, string(op)),
				instance,
			)
		}
	}

	instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)

	// Generate the config maps for the various services
	configMapVars := make(map[string]env.Setter)
	err = r.generateServiceConfigMaps(ctx, h, instance, &configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %v", err)
	}
	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, configMapVars)
	configHash := ""
	for _, hashEnv := range mergedMapVars {
		configHash = configHash + hashEnv.Value
	}

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	// Define a new Job object
	jobDef := mariadb.DbInitJob(instance)

	DBInitJob := job.NewJob(
		jobDef,
		"dbinit",
		time.Duration(5)*time.Second,
		instance.Status.DbInitHash,
	)

	ctrlResult, err := DBInitJob.DoJob(
		ctx,
		h,
	)
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBInitializedCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			databasev1beta1.MariaDBInitializedRunningMessage))
		return ctrlResult, nil
	}
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			databasev1beta1.MariaDBInitializedCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			databasev1beta1.MariaDBInitializedErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if DBInitJob.HasChanged() {
		instance.Status.DbInitHash = DBInitJob.GetHash()
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.DbInitHash))
	}
	err = job.DeleteAllSucceededJobs(ctx, h, []string{instance.Status.DbInitHash})
	if err != nil {
		return ctrlResult, err
	}
	instance.Status.Conditions.MarkTrue(databasev1beta1.MariaDBInitializedCondition, databasev1beta1.MariaDBInitializedReadyMessage)

	// Pod
	pod := mariadb.Pod(instance, configHash)

	op, err = controllerutil.CreateOrPatch(ctx, r.Client, pod, func() error {
		pod.Spec.Containers[0].Image = instance.Spec.ContainerImage
		err := controllerutil.SetControllerReference(instance, pod, r.Scheme)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DeploymentReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	if op != controllerutil.OperationResultNone {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))

		util.LogForObject(
			h,
			fmt.Sprintf("Pod %s successfully reconciled - operation: %s", pod.Name, string(op)),
			instance,
		)
	}

	if pod.Status.Phase == corev1.PodRunning {
		instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)
	}

	return ctrl.Result{}, nil
}

func (r *MariaDBReconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *databasev1beta1.MariaDB,
	envVars *map[string]env.Setter,
) error {
	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(mariadb.ServiceName), map[string]string{})
	templateParameters := make(map[string]interface{})

	// ConfigMaps for mariadb
	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:               "mariadb-" + instance.Name,
			Namespace:          instance.Namespace,
			Type:               util.TemplateTypeScripts,
			InstanceType:       instance.Kind,
			AdditionalTemplate: map[string]string{},
			ConfigOptions:      templateParameters,
			Labels:             cmLabels,
		},
	}

	err := configmap.EnsureConfigMaps(ctx, h, instance, cms, envVars)

	if err != nil {
		// FIXME error conditions here
		return err
	}

	return nil
}

// SetupWithManager -
func (r *MariaDBReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDB{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
