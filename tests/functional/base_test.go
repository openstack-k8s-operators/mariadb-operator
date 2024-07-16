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

package functional_test

import (
	"time"

	. "github.com/onsi/gomega" //revive:disable:dot-imports

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

const (
	timeout  = 10 * time.Second
	interval = timeout / 100
)

func CreateGaleraConfig(namespace string, spec map[string]interface{}) client.Object {
	name := uuid.New().String()

	raw := map[string]interface{}{
		"apiVersion": "mariadb.openstack.org/v1beta1",
		"kind":       "Galera",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}

	return th.CreateUnstructured(raw)
}

func GetDefaultGaleraSpec() map[string]interface{} {
	return map[string]interface{}{
		"replicas":       1,
		"logToDisk":      false,
		"secret":         "osp-secret",
		"storageClass":   "local-storage",
		"storageRequest": "500M",
	}
}

func GetGalera(name types.NamespacedName) *mariadbv1.Galera {
	instance := &mariadbv1.Galera{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}
