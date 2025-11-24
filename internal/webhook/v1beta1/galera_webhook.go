/*
Copyright 2025.

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

// Package v1beta1 implements webhook handlers for MariaDB v1beta1 API resources.
package v1beta1

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	mariadbv1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

// ErrInvalidObjectType is returned when an unexpected object type is provided to a webhook handler
var ErrInvalidObjectType = errors.New("invalid object type")

// nolint:unused
// log is for logging in this package.
var galeralog = logf.Log.WithName("galera-resource")

// SetupGaleraWebhookWithManager registers the webhook for Galera in the manager.
func SetupGaleraWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&mariadbv1beta1.Galera{}).
		WithValidator(&GaleraCustomValidator{}).
		WithDefaulter(&GaleraCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-mariadb-openstack-org-v1beta1-galera,mutating=true,failurePolicy=fail,sideEffects=None,groups=mariadb.openstack.org,resources=galeras,verbs=create;update,versions=v1beta1,name=mgalera-v1beta1.kb.io,admissionReviewVersions=v1

// GaleraCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Galera when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type GaleraCustomDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

var _ webhook.CustomDefaulter = &GaleraCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Galera.
func (d *GaleraCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	galera, ok := obj.(*mariadbv1beta1.Galera)

	if !ok {
		return fmt.Errorf("expected an Galera object but got %T: %w", obj, ErrInvalidObjectType)
	}
	galeralog.Info("Defaulting for Galera", "name", galera.GetName())

	// Call the Default method on the Galera type
	galera.Default()

	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-mariadb-openstack-org-v1beta1-galera,mutating=false,failurePolicy=fail,sideEffects=None,groups=mariadb.openstack.org,resources=galeras,verbs=create;update,versions=v1beta1,name=vgalera-v1beta1.kb.io,admissionReviewVersions=v1

// GaleraCustomValidator struct is responsible for validating the Galera resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type GaleraCustomValidator struct {
	// TODO(user): Add more fields as needed for validation
}

var _ webhook.CustomValidator = &GaleraCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Galera.
func (v *GaleraCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	galera, ok := obj.(*mariadbv1beta1.Galera)
	if !ok {
		return nil, fmt.Errorf("expected a Galera object but got %T: %w", obj, ErrInvalidObjectType)
	}
	galeralog.Info("Validation for Galera upon creation", "name", galera.GetName())

	// Call the ValidateCreate method on the Galera type
	return galera.ValidateCreate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Galera.
func (v *GaleraCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	galera, ok := newObj.(*mariadbv1beta1.Galera)
	if !ok {
		return nil, fmt.Errorf("expected a Galera object for the newObj but got %T: %w", newObj, ErrInvalidObjectType)
	}
	galeralog.Info("Validation for Galera upon update", "name", galera.GetName())

	// Call the ValidateUpdate method on the Galera type
	return galera.ValidateUpdate(oldObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Galera.
func (v *GaleraCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	galera, ok := obj.(*mariadbv1beta1.Galera)
	if !ok {
		return nil, fmt.Errorf("expected a Galera object but got %T: %w", obj, ErrInvalidObjectType)
	}
	galeralog.Info("Validation for Galera upon deletion", "name", galera.GetName())

	// Call the ValidateDelete method on the Galera type
	return galera.ValidateDelete()
}
