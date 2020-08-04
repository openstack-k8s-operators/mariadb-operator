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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MariaDBSchemaSpec defines the desired state of MariaDBSchema
type MariaDBSchemaSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Password string `json:"password,omitempty"`

	Name string `json:"name,omitempty"`
}

// MariaDBSchemaStatus defines the observed state of MariaDBSchema
type MariaDBSchemaStatus struct {
	Completed bool `json:"completed,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MariaDBSchema is the Schema for the mariadbschemas API
type MariaDBSchema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MariaDBSchemaSpec   `json:"spec,omitempty"`
	Status MariaDBSchemaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MariaDBSchemaList contains a list of MariaDBSchema
type MariaDBSchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MariaDBSchema `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MariaDBSchema{}, &MariaDBSchemaList{})
}
