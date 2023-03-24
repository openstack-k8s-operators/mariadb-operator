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

package v1beta1

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// MariaDBDefaults -
type MariaDBDefaults struct {
	ContainerImageURL string
}

var mariaDBDefaults MariaDBDefaults

// log is for logging in this package.
var mariadblog = logf.Log.WithName("mariadb-resource")

// SetupMariaDBDefaults - initialize MariaDB spec defaults for use with either internal or external webhooks
func SetupMariaDBDefaults(defaults MariaDBDefaults) {
	mariaDBDefaults = defaults
	mariadblog.Info("MariaDB defaults initialized", "defaults", defaults)
}

// SetupWebhookWithManager sets up the webhook with the Manager
func (r *MariaDB) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-mariadb-openstack-org-v1beta1-mariadb,mutating=true,failurePolicy=fail,sideEffects=None,groups=mariadb.openstack.org,resources=mariadbs,verbs=create;update,versions=v1beta1,name=mmariadb.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &MariaDB{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *MariaDB) Default() {
	mariadblog.Info("default", "name", r.Name)

	r.Spec.Default()
}

// Default - set defaults for this MariaDB spec
func (spec *MariaDBSpec) Default() {
	if spec.ContainerImage == "" {
		spec.ContainerImage = mariaDBDefaults.ContainerImageURL
	}
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-mariadb-openstack-org-v1beta1-mariadb,mutating=false,failurePolicy=fail,sideEffects=None,groups=mariadb.openstack.org,resources=mariadbs,verbs=create;update,versions=v1beta1,name=vmariadb.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &MariaDB{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *MariaDB) ValidateCreate() error {
	mariadblog.Info("validate create", "name", r.Name)

	// TODO(user): fill in your validation logic upon object creation.
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *MariaDB) ValidateUpdate(old runtime.Object) error {
	mariadblog.Info("validate update", "name", r.Name)

	// TODO(user): fill in your validation logic upon object update.
	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *MariaDB) ValidateDelete() error {
	mariadblog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}
