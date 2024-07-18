/*
Copyright 2024 Red Hat

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
	"testing"

	. "github.com/onsi/gomega" //revive:disable:dot-imports
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"k8s.io/utils/ptr"
)

func TestCreateDatabaseClientConfig(t *testing.T) {
	tests := []struct {
		name         string
		db           *Database
		service      *tls.Service
		wantStmts    []string
		excludeStmts []string
	}{
		{
			name:         "DB no TLS",
			db:           &Database{tlsSupport: false},
			service:      &tls.Service{},
			wantStmts:    []string{"ssl=0"},
			excludeStmts: []string{"ssl-cert=", "ssl-key="},
		},
		{
			name:    "DB TLS - only default CA Secret",
			db:      &Database{tlsSupport: true},
			service: &tls.Service{},
			wantStmts: []string{
				"ssl=1",
				"ssl-ca=/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"},
			excludeStmts: []string{"ssl-cert=", "ssl-key="},
		},
		{
			name:    "DB TLS - custom CA",
			db:      &Database{tlsSupport: true},
			service: &tls.Service{CaMount: ptr.To("/some/path/ca.crt")},
			wantStmts: []string{
				"ssl=1",
				"ssl-ca=/some/path/ca.crt"},
			excludeStmts: []string{"ssl-cert=", "ssl-key="},
		},
		{
			name: "DB TLS - cert and key path provided",
			db:   &Database{tlsSupport: true},
			service: &tls.Service{
				CertMount: ptr.To("/some/path/tls.crt"),
				KeyMount:  ptr.To("/some/path/tls.key")},
			wantStmts: []string{
				"ssl=1",
				"ssl-cert=/some/path/tls.crt",
				"ssl-key=/some/path/tls.key",
				"ssl-ca=/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"},
			excludeStmts: []string{},
		},
		{
			name: "DB TLS - cert, key and custom CA provided",
			db:   &Database{tlsSupport: true},
			service: &tls.Service{
				CertMount: ptr.To("/some/path/tls.crt"),
				KeyMount:  ptr.To("/some/path/tls.key"),
				CaMount:   ptr.To("/some/path/ca.crt")},
			wantStmts: []string{
				"ssl=1",
				"ssl-cert=/some/path/tls.crt",
				"ssl-key=/some/path/tls.key",
				"ssl-ca=/some/path/ca.crt"},
			excludeStmts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			configStr := tt.db.GetDatabaseClientConfig(tt.service)

			for _, stmt := range tt.wantStmts {
				g.Expect(configStr).To(ContainSubstring(stmt))
			}
			for _, stmt := range tt.excludeStmts {
				g.Expect(configStr).ToNot(ContainSubstring(stmt))
			}
		})
	}
}
