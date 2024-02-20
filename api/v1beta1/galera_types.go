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
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// CustomServiceConfigFile name of the additional mariadb config file
	CustomServiceConfigFile = "galera_custom.cnf.in"

	// GaleraContainerImage is the fall-back container image for Galera
	GaleraContainerImage = "quay.io/podified-antelope-centos9/openstack-mariadb:current-podified"
)

// AdoptionRedirectSpec defines redirection to a different DB instance during Adoption
type AdoptionRedirectSpec struct {
	// MariaDB host to redirect to (IP or name)
	Host string `json:"host,omitempty"`
}

// GaleraSpec defines the desired state of Galera
type GaleraSpec struct {
	GaleraSpecCore `json:",inline"`
	// Name of the galera container image to run (will be set to environmental default if empty)
	// +kubebuilder:validation:Required
	ContainerImage string `json:"containerImage"`
}

// GaleraSpec defines the desired state of Galera
type GaleraSpecCore struct {
	// Name of the secret to look for password keys
	// +kubebuilder:validation:Required
	Secret string `json:"secret"`
	// Storage class to host the mariadb databases
	// +kubebuilder:validation:Required
	StorageClass string `json:"storageClass"`
	// Storage size allocated for the mariadb databases
	// +kubebuilder:validation:Required
	StorageRequest string `json:"storageRequest"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +kubebuilder:validation:Enum=1;3
	// Size of the galera cluster deployment
	Replicas *int32 `json:"replicas"`
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
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	// TLS settings for MySQL service and internal Galera replication
	TLS tls.SimpleService `json:"tls,omitempty"`
	// +kubebuilder:validation:Optional
	// When TLS is configured, only allow connections to the DB over TLS
	DisableNonTLSListeners bool `json:"disableNonTLSListeners,omitempty"`
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
	// Name of the node that can safely bootstrap a cluster
	SafeToBootstrap string `json:"safeToBootstrap,omitempty"`
	// Is the galera cluster currently running
	// +kubebuilder:default=false
	Bootstrapped bool `json:"bootstrapped"`
	// Does the galera cluster requires to be stopped globally
	// +kubebuilder:default=false
	StopRequired bool `json:"stopRequired"`
	// Map of properties that require full cluster restart if changed
	ClusterProperties map[string]string `json:"clusterProperties,omitempty"`
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

// SetupDefaults - initializes any CRD field defaults based on environment variables (the defaulting mechanism itself is implemented via webhooks)
func SetupDefaults() {
	// Acquire environmental defaults and initialize Keystone defaults with them
	galeraDefaults := GaleraDefaults{
		ContainerImageURL: util.GetEnvVar("RELATED_IMAGE_MARIADB_IMAGE_URL_DEFAULT", GaleraContainerImage),
	}

	SetupGaleraDefaults(galeraDefaults)
}
