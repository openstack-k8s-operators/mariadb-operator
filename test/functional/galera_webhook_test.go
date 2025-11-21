/*
Copyright 2023.

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
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Galera webhook", func() {
	var galeraName types.NamespacedName

	When("a default Galera gets created", func() {
		BeforeEach(func() {
			galera := CreateGaleraConfig(namespace, GetDefaultGaleraSpec())
			galeraName.Name = galera.GetName()
			galeraName.Namespace = galera.GetNamespace()
			DeferCleanup(th.DeleteInstance, galera)
		})

		It("should have created a Galera", func() {
			Eventually(func(_ Gomega) {
				GetGalera(galeraName)
			}, timeout, interval).Should(Succeed())
		})
	})

	When("a Galera gets created with a name longer then 46 chars", func() {
		It("gets blocked by the webhook and fail", func() {

			raw := map[string]any{
				"apiVersion": "mariadb.openstack.org/v1beta1",
				"kind":       "Galera",
				"metadata": map[string]any{
					"name":      "foo-1234567890-1234567890-1234567890-1234567890",
					"namespace": namespace,
				},
				"spec": GetDefaultGaleraSpec(),
			}

			unstructuredObj := &unstructured.Unstructured{Object: raw}
			_, err := controllerutil.CreateOrPatch(
				th.Ctx, th.K8sClient, unstructuredObj, func() error { return nil })
			Expect(err).To(HaveOccurred())
		})
	})
})
