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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DbCreateHash hash
	DbCreateHash = "dbcreate"

	// DbDeleteHash hash
	DbDeleteHash = "dbdelete"
)

// MariaDBDatabaseSpec defines the desired state of MariaDBDatabase
type MariaDBDatabaseSpec struct {
	// Name of secret which contains DatabasePassword
	Secret string `json:"secret,omitempty"`
	// Name of the database in MariaDB
	Name string `json:"name,omitempty"`
	// +kubebuilder:default=utf8
	// Default character set for this database
	DefaultCharacterSet string `json:"defaultCharacterSet"`
	// +kubebuilder:default=utf8_general_ci
	// Default collation for this database
	DefaultCollation string `json:"defaultCollation"`
}

// MariaDBDatabaseStatus defines the observed state of MariaDBDatabase
type MariaDBDatabaseStatus struct {
	Completed bool `json:"completed,omitempty"`
	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// MariaDBDatabase is the Schema for the mariadbdatabases API
type MariaDBDatabase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MariaDBDatabaseSpec   `json:"spec,omitempty"`
	Status MariaDBDatabaseStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MariaDBDatabaseList contains a list of MariaDBDatabase
type MariaDBDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MariaDBDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MariaDBDatabase{}, &MariaDBDatabaseList{})
}

const (
	// DatabaseUserPasswordKey - key in secret which holds the service user DB password
	DatabaseUserPasswordKey = "DatabasePassword"
	// DatabaseAdminPasswordKey - key in secret which holds the admin user password
	DatabaseAdminPasswordKey = "AdminPassword"
)

// Database -
type Database struct {
	database         *MariaDBDatabase
	databaseHostname string
	databaseName     string
	databaseUser     string
	secret           string
	labels           map[string]string
	name             string
	namespace        string
}
