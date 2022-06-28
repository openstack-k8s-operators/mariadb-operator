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

	common "github.com/openstack-k8s-operators/lib-common/pkg/common"
	helper "github.com/openstack-k8s-operators/lib-common/pkg/helper"
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

	// PVC
	pvc := mariadb.Pvc(instance, r.Scheme)
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

	service := mariadb.Service(instance, r.Scheme)

	op, err = controllerutil.CreateOrPatch(ctx, r.Client, service, func() error {
		err := controllerutil.SetControllerReference(instance, service, r.Scheme)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		// FIXME: add error condition
		return ctrl.Result{}, err
	}

	if op != controllerutil.OperationResultNone {
		common.LogForObject(
			r,
			fmt.Sprintf("Service %s successfully reconciled - operation: %s", service.Name, string(op)),
			instance,
		)
	}

	// Generate the config maps for the various services
	configMapVars := make(map[string]common.EnvSetter)
	err = r.generateServiceConfigMaps(ctx, instance, &configMapVars)
	if err != nil {
		return ctrl.Result{}, err
	}
	mergedMapVars := common.MergeEnvs([]corev1.EnvVar{}, configMapVars)
	configHash := ""
	for _, hashEnv := range mergedMapVars {
		configHash = configHash + hashEnv.Value
	}

	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %v", err)
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

	// Define a new Job object
	jobDef := mariadb.DbInitJob(instance)

	job := common.NewJob(
		jobDef,
		"dbinit",
		false,
		5,
		instance.Status.DbInitHash,
	)

	ctrlResult, err := job.DoJob(
		ctx,
		helper,
	)
	if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if job.HasChanged() {
		instance.Status.DbInitHash = job.GetHash()
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info(fmt.Sprintf("Job %s hash added - %s", jobDef.Name, instance.Status.DbInitHash))
	}

	// Pod
	pod := mariadb.Pod(instance, r.Scheme, configHash)

	op, err = controllerutil.CreateOrPatch(ctx, r.Client, pod, func() error {
		pod.Spec.Containers[0].Image = instance.Spec.ContainerImage
		err := controllerutil.SetControllerReference(instance, pod, r.Scheme)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		//FIXME: error conditions
		return ctrl.Result{}, err
	}

	if op != controllerutil.OperationResultNone {
		common.LogForObject(
			r,
			fmt.Sprintf("Pod %s successfully reconciled - operation: %s", pod.Name, string(op)),
			instance,
		)
	}

	return ctrl.Result{}, nil
}

func (r *MariaDBReconciler) generateServiceConfigMaps(
	ctx context.Context,
	instance *databasev1beta1.MariaDB,
	envVars *map[string]common.EnvSetter,
) error {
	// FIXME: use common.GetLabels?
	cmLabels := mariadb.GetLabels(instance.Name)
	templateParameters := make(map[string]interface{})

	// ConfigMaps for mariadb
	cms := []common.Template{
		// ScriptsConfigMap
		{
			Name:               "mariadb-" + instance.Name,
			Namespace:          instance.Namespace,
			Type:               common.TemplateTypeScripts,
			InstanceType:       instance.Kind,
			AdditionalTemplate: map[string]string{},
			ConfigOptions:      templateParameters,
			Labels:             cmLabels,
		},
	}

	err := common.EnsureConfigMaps(ctx, r, instance, cms, envVars)

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
