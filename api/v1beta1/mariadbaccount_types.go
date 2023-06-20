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
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// AccountCreateHash hash
	AccountCreateHash = "accountcreate"

	// AccountDeleteHash hash
	AccountDeleteHash = "accountdelete"
)

// MariaDBAccountSpec defines the desired state of MariaDBAccount
type MariaDBAccountSpec struct {
	// UserName for new account
	// +kubebuilder:validation:Required
	UserName string `json:"userName"`

	// Name of secret which contains DatabasePassword
	// +kubebuilder:validation:Required
	Secret string `json:"secret"`

	// Account must use TLS to connect to the database
	// +kubebuilder:default=false
	RequireTLS bool `json:"requireTLS"`
}

// MariaDBAccountStatus defines the observed state of MariaDBAccount
type MariaDBAccountStatus struct {
	// Deployment Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// MariaDBAccount is the Schema for the mariadbaccounts API
type MariaDBAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MariaDBAccountSpec   `json:"spec,omitempty"`
	Status MariaDBAccountStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MariaDBAccountList contains a list of MariaDBAccount
type MariaDBAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MariaDBAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MariaDBAccount{}, &MariaDBAccountList{})
}
