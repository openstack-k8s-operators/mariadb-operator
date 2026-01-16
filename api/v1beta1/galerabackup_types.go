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

// GaleraBackupSpec defines the desired state of GaleraBackup
type GaleraBackupSpec struct {
	// Galera cluster to backup
	DatabaseInstance string `json:"databaseInstance,omitempty"`
	// Storage volume for backup data
	StorageClass string `json:"storageClass,omitempty"`
	// Storage Request for backup data
	StorageRequest string `json:"storageRequest,omitempty"`
	// Configuration of storage used for intermediate data transfer
	TransferStorage *GaleraBackupTransferStorageSpec `json:"transferStorage,omitempty"`
	// The schedule in Cron format
	// +kubebuilder:default="@daily"
	Schedule string `json:"schedule"`
	// Time duration after which old backups must be reclaimed on disk
	// +optional
	Retention *metav1.Duration `json:"retention,omitempty"`
}

type GaleraBackupTransferStorageSpec struct {
	// Storage volume for backup data
	StorageClass string `json:"storageClass,omitempty"`
	// Storage Request for backup data
	StorageRequest string `json:"storageRequest"`
}

// GaleraBackupStatus defines the observed state of GaleraBackup
type GaleraBackupStatus struct {
	// Map of hashes to track input changes
	Hash map[string]string `json:"hash,omitempty"`
	// Deployment Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`
	// ObservedGeneration - the most recent generation observed for this
	// service. If the observed generation is less than the spec generation,
	// then the controller has not processed the latest changes injected by
	// the opentack-operator in the top-level CR (e.g. the ContainerImage)
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[0].status",description="Ready"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// GaleraBackup is the Schema for the galerabackups API
type GaleraBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GaleraBackupSpec   `json:"spec,omitempty"`
	Status GaleraBackupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GaleraBackupList contains a list of GaleraBackup
type GaleraBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GaleraBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GaleraBackup{}, &GaleraBackupList{})
}

// // IsReady - returns true if service is ready to serve requests
// func (instance Galera) IsReady() bool {
// 	return instance.Status.Conditions.IsTrue(condition.DeploymentReadyCondition)
// }

// RbacConditionsSet - sets the conditions for the rbac object
func (instance GaleraBackup) RbacConditionsSet(c *condition.Condition) {
	instance.Status.Conditions.Set(c)
}

// RbacNamespace - returns the namespace name
func (instance GaleraBackup) RbacNamespace() string {
	return instance.Namespace
}

// RbacResourceName - return the name to be used for rbac objects (serviceaccount, role, rolebinding)
func (instance GaleraBackup) RbacResourceName() string {
	return "galerabackup-" + instance.Name
}
