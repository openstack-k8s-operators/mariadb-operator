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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	common_webhook "github.com/openstack-k8s-operators/lib-common/modules/common/webhook"
)

// log is for logging in this package.
var galeralog = logf.Log.WithName("galera-resource")

// GaleraDefaults -
type GaleraDefaults struct {
	ContainerImageURL string
}

var galeraDefaults GaleraDefaults

// SetupWebhookWithManager sets up the webhook with the Manager
func (r *Galera) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-mariadb-openstack-org-v1beta1-galera,mutating=true,failurePolicy=fail,sideEffects=None,groups=mariadb.openstack.org,resources=galeras,verbs=create;update,versions=v1beta1,name=mgalera.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &Galera{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *Galera) Default() {
	galeralog.Info("default", "name", r.Name)

	r.Spec.Default()
}

// Default - set defaults for this GaleraSpec
func (spec *GaleraSpec) Default() {
	// only image validations go here
	if spec.ContainerImage == "" {
		spec.ContainerImage = galeraDefaults.ContainerImageURL
	}
	spec.GaleraSpecCore.Default()
}

// Default - set defaults for the GaleraSpecCore. This version is used by OpenStackControlplane
func (spec *GaleraSpecCore) Default() {
	// nothing here yet
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-mariadb-openstack-org-v1beta1-galera,mutating=false,failurePolicy=fail,sideEffects=None,groups=mariadb.openstack.org,resources=galeras,verbs=create;update,versions=v1beta1,name=vgalera.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Galera{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *Galera) ValidateCreate() (admission.Warnings, error) {
	galeralog.Info("validate create", "name", r.Name)

	allWarn := []string{}
	basePath := field.NewPath("spec")

	allErrs := common_webhook.ValidateDNS1123Label(
		field.NewPath("metadata").Child("name"),
		[]string{r.Name},
		CrMaxLengthCorrection) // omit issue with  statefulset pod label "controller-revision-hash": "<statefulset_name>-<hash>"

	warn, err := r.Spec.ValidateCreate(basePath)

	if err != nil {
		allErrs = append(allErrs, err...)
	}

	if len(warn) != 0 {
		allWarn = append(allWarn, warn...)
	}

	if len(allErrs) != 0 {
		return allWarn, apierrors.NewInvalid(GroupVersion.WithKind("Galera").GroupKind(), r.Name, allErrs)
	}

	return allWarn, nil
}

// ValidateCreate - Exported function wrapping non-exported validate functions,
// this function can be called externally to validate an KeystoneAPI spec.
func (spec *GaleraSpec) ValidateCreate(basePath *field.Path) (admission.Warnings, field.ErrorList) {
	return spec.GaleraSpecCore.ValidateCreate(basePath)
}

// ValidateCreate -
func (spec *GaleraSpecCore) ValidateCreate(basePath *field.Path) (admission.Warnings, field.ErrorList) {
	var allErrs field.ErrorList
	allWarn := []string{}

	warn, _ := common_webhook.ValidateStorageRequest(basePath, spec.StorageRequest, storageRequestProdMin, false)
	allWarn = append(allWarn, warn...)

	warn = spec.ValidateGaleraReplicas(basePath)
	allWarn = append(allWarn, warn...)

	return allWarn, allErrs
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *Galera) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	allWarn := []string{}
	galeralog.Info("validate update", "name", r.Name)

	oldGalera, ok := old.(*Galera)
	if !ok || oldGalera == nil {
		return nil, apierrors.NewInternalError(fmt.Errorf("unable to convert existing object"))
	}

	basePath := field.NewPath("spec")
	warn := r.Spec.ValidateGaleraReplicas(basePath)
	allWarn = append(allWarn, warn...)

	// TODO(user): fill in your validation logic upon object update.
	return allWarn, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *Galera) ValidateDelete() (admission.Warnings, error) {
	galeralog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

// SetupGaleraDefaults - initialize MariaDB spec defaults for use with either internal or external webhooks
func SetupGaleraDefaults(defaults GaleraDefaults) {
	galeraDefaults = defaults
	galeralog.Info("Galera defaults initialized", "defaults", defaults)
}

// SetupGaleraDefaults - Check whether replica count is valid for quorum
func (spec *GaleraSpecCore) ValidateGaleraReplicas(basePath *field.Path) admission.Warnings {
	replicas := int(*spec.Replicas)
	if replicas > 0 && (replicas%2 == 0) {
		res := fmt.Sprintf("%s: %d is not appropriate for quorum! Use an odd value!",
			basePath.Child("replicas").String(), replicas)
		return []string{res}
	} else {
		return nil
	}
}
