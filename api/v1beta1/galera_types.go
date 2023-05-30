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
	// CustomServiceConfigFile name of the additional mariadb config file
	CustomServiceConfigFile = "galera_custom.cnf.in"
)

// GaleraSpec defines the desired state of Galera
type GaleraSpec struct {
	// Name of the secret to look for password keys
	// +kubebuilder:validation:Required
	Secret string `json:"secret"`
	// Storage class to host the mariadb databases
	// +kubebuilder:validation:Required
	StorageClass string `json:"storageClass"`
	// Storage size allocated for the mariadb databases
	// +kubebuilder:validation:Required
	StorageRequest string `json:"storageRequest"`
	// Name of the galera container image to run (will be set to environmental default if empty)
	ContainerImage string `json:"containerImage"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +kubebuilder:validation:Enum=1;3
	// Size of the galera cluster deployment
	Replicas int32 `json:"replicas"`
	// +kubebuilder:validation:Optional
	// NodeSelector to target subset of worker nodes running this service
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +kubebuilder:validation:Optional
	// Customize config using this parameter to change service defaults,
	// or overwrite rendered information using raw MariaDB config format.
	// The content gets added to /etc/my.cnf.d/galera_custom.cnf
	CustomServiceConfig string `json:"customServiceConfig,omitempty"`
	// +kubebuilder:validation:Optional
	// Adoption configuration
	AdoptionRedirect AdoptionRedirectSpec `json:"adoptionRedirect"`
}

// GaleraAttributes holds startup information for a Galera host
type GaleraAttributes struct {
	// Last recorded replication sequence number in the DB
	Seqno string `json:"seqno"`
	// Gcomm URI used to connect to the galera cluster
	Gcomm string `json:"gcomm,omitempty"`
	// Identifier of the container at the time the gcomm URI was injected
	ContainerID string `json:"containerID,omitempty"`
}

// GaleraStatus defines the observed state of Galera
type GaleraStatus struct {
	// A map of database node attributes for each pod
	Attributes map[string]GaleraAttributes `json:"attributes,omitempty"`
	// Is the galera cluster currently running
	// +kubebuilder:default=false
	Bootstrapped bool `json:"bootstrapped"`
	// Hash of the configuration files
	// +kubebuilder:default=""
	ConfigHash string `json:"configHash"`
	// Deployment Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[0].status",description="Ready"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// Galera is the Schema for the galeras API
type Galera struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GaleraSpec   `json:"spec,omitempty"`
	Status GaleraStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GaleraList contains a list of Galera
type GaleraList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Galera `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Galera{}, &GaleraList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance Galera) IsReady() bool {
	return instance.Status.Conditions.IsTrue(condition.DeploymentReadyCondition)
}

// RbacConditionsSet - sets the conditions for the rbac object
func (instance Galera) RbacConditionsSet(c *condition.Condition) {
	instance.Status.Conditions.Set(c)
}

// RbacNamespace - returns the namespace name
func (instance Galera) RbacNamespace() string {
	return instance.Namespace
}

// RbacResourceName - return the name to be used for rbac objects (serviceaccount, role, rolebinding)
func (instance Galera) RbacResourceName() string {
	return "galera-" + instance.Name
}
