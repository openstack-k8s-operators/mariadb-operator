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
	"reflect"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	util "github.com/openstack-k8s-operators/lib-common/pkg/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg"
	"k8s.io/client-go/kubernetes"
)

// MariaDBReconciler reconciles a MariaDB object
type MariaDBReconciler struct {
	Client  client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// +kubebuilder:rbac:groups=database.openstack.org,resources=mariadbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=database.openstack.org,resources=mariadbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;create;update;delete;
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;create;update;delete;
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;create;update;delete;

// Reconcile reconcile mariadb API requests
func (r *MariaDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("mariadb", req.NamespacedName)

	// Fetch the MariaDB instance
	instance := &databasev1beta1.MariaDB{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			// For additional cleanup logic use finalizers. Return and don't requeue.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// PVC
	pvc, err := mariadb.Pvc(instance, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}
	foundPvc := &corev1.PersistentVolumeClaim{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}, foundPvc)
	if err != nil && k8s_errors.IsNotFound(err) {

		r.Log.Info("Creating a new Pvc", "PersistentVolumeClaim.Namespace", pvc.Namespace, "Service.Name", pvc.Name)
		err = r.Client.Create(context.TODO(), pvc)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	} else if err != nil {
		return ctrl.Result{}, err
	}

	service, err := mariadb.Service(instance, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Check if this Service already exists
	foundService := &corev1.Service{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)
	if err != nil && k8s_errors.IsNotFound(err) {

		r.Log.Info("Creating a new Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
		err = r.Client.Create(context.TODO(), service)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// ConfigMap
	configMap, err := mariadb.ConfigMap(instance, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Check if this ConfigMap already exists
	foundConfigMap := &corev1.ConfigMap{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, foundConfigMap)
	if err != nil && k8s_errors.IsNotFound(err) {
		r.Log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "Job.Name", configMap.Name)
		err = r.Client.Create(context.TODO(), configMap)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else if !reflect.DeepEqual(configMap.Data, foundConfigMap.Data) {
		r.Log.Info("Updating ConfigMap")
		foundConfigMap.Data = configMap.Data
		err = r.Client.Update(context.TODO(), foundConfigMap)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	configHash, err := util.ObjectHash(configMap)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting configMap hash: %v", err)
	}

	// Define a new Job object
	job, err := mariadb.DbInitJob(instance, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}
	dbInitHash, err := util.ObjectHash(job)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error calculating DB init hash: %v", err)
	}

	if instance.Status.DbInitHash != dbInitHash {
		requeue, err := util.EnsureJob(job, r.Client, r.Log)
		r.Log.Info("Running DB init")
		if err != nil {
			return ctrl.Result{}, err
		} else if requeue {
			r.Log.Info("Waiting on DB init")
			return ctrl.Result{RequeueAfter: time.Second * 5}, err
		}
	}
	// db init completed... okay to store the hash to disable it
	if err := r.setDbInitHash(instance, dbInitHash); err != nil {
		return ctrl.Result{}, err
	}
	// delete the job
	_, err = util.DeleteJob(job, r.Kclient, r.Log)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Pod
	pod, err := mariadb.Pod(instance, r.Scheme, configHash)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Check if this Pod already exists
	foundPod := &corev1.Pod{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, foundPod)
	if err != nil && k8s_errors.IsNotFound(err) {
		r.Log.Info("Creating a new Pod", "Pod.Namespace", pod.Namespace, "Job.Name", pod.Name)
		err = r.Client.Create(context.TODO(), pod)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	} else if instance.Spec.ContainerImage != foundPod.Spec.Containers[0].Image {
		r.Log.Info("Updating Pod...")
		foundPod.Spec.Containers[0].Image = instance.Spec.ContainerImage
		foundPod.Spec.InitContainers[0].Image = instance.Spec.ContainerImage
		err = r.Client.Update(context.TODO(), foundPod)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	return ctrl.Result{}, nil
}

func (r *MariaDBReconciler) setDbInitHash(db *databasev1beta1.MariaDB, hashStr string) error {

	if hashStr != db.Status.DbInitHash {
		db.Status.DbInitHash = hashStr
		if err := r.Client.Status().Update(context.TODO(), db); err != nil {
			return err
		}
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
