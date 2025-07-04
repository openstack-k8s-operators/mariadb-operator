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

	// DbRootPassword selector for galera root account
	DbRootPasswordSelector = "DbRootPassword"

	// DatabasePassword selector for MariaDBAccount->Secret
	DatabasePasswordSelector = "DatabasePassword"
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

	// +kubebuilder:validation:Enum=User;System
	// +kubebuilder:default=User
	AccountType AccountType `json:"accountType,omitempty"`
}

type AccountType string

const (
	User   AccountType = "User"
	System AccountType = "System"
)

// MariaDBAccountStatus defines the observed state of MariaDBAccount
type MariaDBAccountStatus struct {
	// the Secret that's currently in use for the account.
	// keeping a handle to this secret allows us to remove its finalizer
	// when it's replaced with a new one.    It also is useful for storing
	// the current "root" secret separate from a newly proposed one which is
	// needed when changing the database root password.
	CurrentSecret string `json:"currentSecret,omitempty"`

	// Deployment Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
//+kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

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

func (mariadbAccount MariaDBAccount) IsSystemAccount() bool {
	return mariadbAccount.Spec.AccountType == System
}

func (mariadbAccount MariaDBAccount) IsUserAccount() bool {
	return mariadbAccount.Spec.AccountType == "" || mariadbAccount.Spec.AccountType == User
}
