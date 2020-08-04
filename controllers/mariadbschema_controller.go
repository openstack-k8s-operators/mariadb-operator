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

	"github.com/go-logr/logr"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg"

	"time"
)

// MariaDBSchemaReconciler reconciles a MariaDBSchema object
type MariaDBSchemaReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=database.openstack.org,resources=mariadbschemas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=database.openstack.org,resources=mariadbschemas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=database.openstack.org,resources=mariadbs/status,verbs=get;list
// +kubebuilder:rbac:groups=database.openstack.org,resources=mariadbs/status,verbs=get;list
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;create;update;delete;
func (r *MariaDBSchemaReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("mariadbschema", req.NamespacedName)

	// Fetch the MariaDBSchema instance
	instance := &databasev1beta1.MariaDBSchema{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, instance)
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

	// Fetch the MariaDB instance from which we'll pull the credentials
	db := &databasev1beta1.MariaDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.ObjectMeta.Labels["dbName"],
			Namespace: req.Namespace,
		},
	}
	objectKey, err := client.ObjectKeyFromObject(db)
	err = r.Client.Get(context.TODO(), objectKey, db)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			r.Log.Info("No DB found for label 'dbName'.")
		}
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	if db.Status.DbInitHash == "" {
		r.Log.Info("DB initialization not complete. Requeue...")
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	// Define a new Job object (hostname, password, containerImage)
	job := mariadb.DbSchemaJob(instance, db.Name, db.Spec.RootPassword, db.Spec.ContainerImage)

	requeue := true
	if instance.Status.Completed {
		requeue, err = mariadb.EnsureJob(job, r.Client, r.Log)
		r.Log.Info("Creating DB schema...")
		if err != nil {
			return ctrl.Result{}, err
		} else if requeue {
			r.Log.Info("Waiting on DB schema creation job...")
			return ctrl.Result{RequeueAfter: time.Second * 5}, err
		}
	}
	// db schema completed... okay to set to completed
	if err := r.setCompleted(instance); err != nil {
		return ctrl.Result{}, err
	}
	// delete the job
	requeue, err = mariadb.DeleteJob(job, r.Client, r.Log)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *MariaDBSchemaReconciler) setCompleted(db *databasev1beta1.MariaDBSchema) error {

	if !db.Status.Completed {
		db.Status.Completed = true
		if err := r.Client.Status().Update(context.TODO(), db); err != nil {
			return err
		}
	}
	return nil
}

func (r *MariaDBSchemaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1beta1.MariaDBSchema{}).
		Complete(r)
}
