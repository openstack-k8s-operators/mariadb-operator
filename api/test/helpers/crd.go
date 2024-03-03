/*
Copyright 2023 Red Hat
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

package helpers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/gomega"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	base "github.com/openstack-k8s-operators/lib-common/modules/common/test/helpers"
)

// TestHelper is a collection of helpers for testing operators. It extends the
// generic TestHelper from modules/test.
type TestHelper struct {
	*base.TestHelper
}

// NewTestHelper returns a TestHelper
func NewTestHelper(
	ctx context.Context,
	k8sClient client.Client,
	timeout time.Duration,
	interval time.Duration,
	logger logr.Logger,
) *TestHelper {
	helper := &TestHelper{}
	helper.TestHelper = base.NewTestHelper(ctx, k8sClient, timeout, interval, logger)
	return helper
}

// CreateGalera creates a new Galera instance with the specified namespace in the Kubernetes cluster.
func (tc *TestHelper) CreateGalera(namespace string, galeraName string, spec mariadbv1.GaleraSpec) types.NamespacedName {
	name := types.NamespacedName{
		Name:      galeraName,
		Namespace: namespace,
	}

	db := &mariadbv1.Galera{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mariadb.openstack.org/v1beta1",
			Kind:       "Galera",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      galeraName,
			Namespace: namespace,
		},
		Spec: spec,
	}

	gomega.Expect(tc.K8sClient.Create(tc.Ctx, db)).Should(gomega.Succeed())

	return name
}

// DeleteGalera deletes a Galera instance from the Kubernetes cluster.
func (tc *TestHelper) DeleteGalera(name types.NamespacedName) {
	gomega.Eventually(func(g gomega.Gomega) {
		db := &mariadbv1.Galera{}
		err := tc.K8sClient.Get(tc.Ctx, name, db)
		// if it is already gone that is OK
		if k8s_errors.IsNotFound(err) {
			return
		}
		g.Expect(err).NotTo(gomega.HaveOccurred())

		g.Expect(tc.K8sClient.Delete(tc.Ctx, db)).Should(gomega.Succeed())

		err = tc.K8sClient.Get(tc.Ctx, name, db)
		g.Expect(k8s_errors.IsNotFound(err)).To(gomega.BeTrue())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())
}

// GetGalera waits for and retrieves a Galera instance from the Kubernetes cluster
func (tc *TestHelper) GetGalera(name types.NamespacedName) *mariadbv1.Galera {
	db := &mariadbv1.Galera{}
	gomega.Eventually(func(g gomega.Gomega) {
		g.Expect(tc.K8sClient.Get(tc.Ctx, name, db)).Should(gomega.Succeed())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())
	return db
}

// CreateDBService creates a k8s Service object that matches with the
// Expectations of lib-common database module as a Service for the MariaDB
func (tc *TestHelper) CreateDBService(namespace string, mariadbCRName string, spec corev1.ServiceSpec) types.NamespacedName {
	// The Name is used as the hostname to access the service. So
	// we generate something unique for the MariaDB CR it represents
	// so we can assert that the correct Service is selected.
	serviceName := "hostname-for-" + mariadbCRName
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			// NOTE(gibi): The lib-common databvase module looks up the
			// Service exposed by MariaDB via these labels.
			Labels: map[string]string{
				"app": "mariadb",
				"cr":  "mariadb-" + mariadbCRName,
			},
		},
		Spec: spec,
	}
	gomega.Expect(tc.K8sClient.Create(tc.Ctx, service)).Should(gomega.Succeed())

	return types.NamespacedName{Name: serviceName, Namespace: namespace}
}

// DeleteDBService The function deletes the Service if exists and wait for it to disappear from the API.
// If the Service does not exists then it is assumed to be successfully deleted.
// Example:
//
//	th.DeleteDBService(types.NamespacedName{Name: "my-service", Namespace: "my-namespace"})
//
// or:
//
//	DeferCleanup(th.DeleteDBService, th.CreateDBService(cell0.MariaDBDatabaseName.Namespace, cell0.MariaDBDatabaseName.Name, serviceSpec))
func (tc *TestHelper) DeleteDBService(name types.NamespacedName) {
	gomega.Eventually(func(g gomega.Gomega) {
		service := &corev1.Service{}
		err := tc.K8sClient.Get(tc.Ctx, name, service)
		// if it is already gone that is OK
		if k8s_errors.IsNotFound(err) {
			return
		}
		g.Expect(err).NotTo(gomega.HaveOccurred())

		g.Expect(tc.K8sClient.Delete(tc.Ctx, service)).Should(gomega.Succeed())

		err = tc.K8sClient.Get(tc.Ctx, name, service)
		g.Expect(k8s_errors.IsNotFound(err)).To(gomega.BeTrue())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())
}

// CreateMariaDBDatabase creates a new MariaDBDatabase instance with the specified namespace in the Kubernetes cluster.
func (tc *TestHelper) CreateMariaDBDatabase(namespace string, dbName string, spec mariadbv1.MariaDBDatabaseSpec) types.NamespacedName {
	name := types.NamespacedName{
		Name:      dbName,
		Namespace: namespace,
	}

	db := &mariadbv1.MariaDBDatabase{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mariadb.openstack.org/v1beta1",
			Kind:       "MariaDBDatabase",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbName,
			Namespace: namespace,
		},
		Spec: spec,
	}

	gomega.Expect(tc.K8sClient.Create(tc.Ctx, db)).Should(gomega.Succeed())

	return name
}

// GetMariaDBDatabase waits for and retrieves a MariaDBDatabase resource from the Kubernetes cluster
//
// Example:
//
//	mariadbDatabase := th.GetMariaDBDatabase(types.NamespacedName{Name: "my-mariadb-database", Namespace: "my-namespace"})
func (tc *TestHelper) GetMariaDBDatabase(name types.NamespacedName) *mariadbv1.MariaDBDatabase {
	instance := &mariadbv1.MariaDBDatabase{}
	gomega.Eventually(func(g gomega.Gomega) {
		g.Expect(tc.K8sClient.Get(tc.Ctx, name, instance)).Should(gomega.Succeed())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())
	return instance
}

// SimulateMariaDBDatabaseCompleted simulates a completed state for a MariaDBDatabase resource in a Kubernetes cluster.
//
// Example:
//
//	th.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Name: "my-mariadb-database", Namespace: "my-namespace"})
//
// or
//
//	DeferCleanup(th.SimulateMariaDBDatabaseCompleted, types.NamespacedName{Name: "my-mariadb-database", Namespace: "my-namespace"})
func (tc *TestHelper) SimulateMariaDBDatabaseCompleted(name types.NamespacedName) {
	gomega.Eventually(func(g gomega.Gomega) {
		db := tc.GetMariaDBDatabase(name)
		db.Status.Completed = true
		db.Status.Conditions.MarkTrue(mariadbv1.MariaDBDatabaseReadyCondition, "Ready")
		// This can return conflict so we have the gomega.Eventually block to retry
		g.Expect(tc.K8sClient.Status().Update(tc.Ctx, db)).To(gomega.Succeed())

	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())

	tc.Logger.Info("Simulated DB completed", "on", name)
}

// SimulateMariaDBTLSDatabaseCompleted simulates a completed state for a MariaDBDatabase resource in a Kubernetes cluster.
//
// Example:
//
//	th.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Name: "my-mariadb-database", Namespace: "my-namespace"})
//
// or
//
//	DeferCleanup(th.SimulateMariaDBTLSDatabaseCompleted, types.NamespacedName{Name: "my-mariadb-database", Namespace: "my-namespace"})
func (tc *TestHelper) SimulateMariaDBTLSDatabaseCompleted(name types.NamespacedName) {
	gomega.Eventually(func(g gomega.Gomega) {
		db := tc.GetMariaDBDatabase(name)
		db.Status.Completed = true
		db.Status.TLSSupport = true
		db.Status.Conditions.MarkTrue(mariadbv1.MariaDBDatabaseReadyCondition, "Ready")
		// This can return conflict so we have the gomega.Eventually block to retry
		g.Expect(tc.K8sClient.Status().Update(tc.Ctx, db)).To(gomega.Succeed())

	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())

	tc.Logger.Info("Simulated DB completed", "on", name)
}

// AssertMariaDBDatabaseDoesNotExist ensures the MariaDBDatabase resource does not exist in a k8s cluster.
func (tc *TestHelper) AssertMariaDBDatabaseDoesNotExist(name types.NamespacedName) {
	instance := &mariadbv1.MariaDBDatabase{}
	gomega.Eventually(func(g gomega.Gomega) {
		err := tc.K8sClient.Get(tc.Ctx, name, instance)
		g.Expect(k8s_errors.IsNotFound(err)).To(gomega.BeTrue())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())
}

// CreateMariaDBAccount creates a new MariaDBAccount instance with the specified namespace in the Kubernetes cluster.
func (tc *TestHelper) CreateMariaDBAccount(namespace string, acctName string, spec mariadbv1.MariaDBAccountSpec) types.NamespacedName {
	name := types.NamespacedName{
		Name:      acctName,
		Namespace: namespace,
	}

	db := &mariadbv1.MariaDBAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mariadb.openstack.org/v1beta1",
			Kind:       "MariaDBAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      acctName,
			Namespace: namespace,
		},
		Spec: spec,
	}

	gomega.Expect(tc.K8sClient.Create(tc.Ctx, db)).Should(gomega.Succeed())

	return name
}

// CreateMariaDBAccountAndSecret creates a new MariaDBAccount and Secret with the specified namespace in the Kubernetes cluster.
func (tc *TestHelper) CreateMariaDBAccountAndSecret(name types.NamespacedName, spec mariadbv1.MariaDBAccountSpec) (*mariadbv1.MariaDBAccount, *corev1.Secret) {
	secretName := fmt.Sprintf("%s-db-secret", name.Name)
	secret := tc.CreateSecret(
		types.NamespacedName{Namespace: name.Namespace, Name: secretName},
		map[string][]byte{
			"DatabasePassword": []byte(fmt.Sprintf("%s123", name.Name)),
		},
	)

	if spec.UserName == "" {
		spec.UserName = fmt.Sprintf("%s_account", strings.Replace(name.Name, "-", "_", -1))
	}
	spec.Secret = secretName

	instance := &mariadbv1.MariaDBAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Spec: spec,
	}

	gomega.Eventually(func(g gomega.Gomega) {
		g.Expect(tc.K8sClient.Create(tc.Ctx, instance)).Should(gomega.Succeed())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())

	tc.Logger.Info(fmt.Sprintf("Created MariaDBAccount %s, username %s, secret %s", name.Name, instance.Spec.UserName, instance.Spec.Secret))

	return instance, secret
}

// GetMariaDBAccount waits for and retrieves a MariaDBAccount resource from the Kubernetes cluster
func (tc *TestHelper) GetMariaDBAccount(name types.NamespacedName) *mariadbv1.MariaDBAccount {
	instance := &mariadbv1.MariaDBAccount{}
	gomega.Eventually(func(g gomega.Gomega) {
		g.Expect(tc.K8sClient.Get(tc.Ctx, name, instance)).Should(gomega.Succeed())
	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())
	return instance
}

// SimulateMariaDBAccountCompleted simulates that the MariaDBAccount is created in the DB
func (tc *TestHelper) SimulateMariaDBAccountCompleted(name types.NamespacedName) {
	gomega.Eventually(func(g gomega.Gomega) {
		acc := tc.GetMariaDBAccount(name)
		acc.Status.Conditions.MarkTrue(mariadbv1.MariaDBAccountReadyCondition, "Ready")
		// This can return conflict so we have the gomega.Eventually block to retry
		g.Expect(tc.K8sClient.Status().Update(tc.Ctx, acc)).To(gomega.Succeed())

	}, tc.Timeout, tc.Interval).Should(gomega.Succeed())

	tc.Logger.Info("Simulated DB Account completed", "on", name)
}
