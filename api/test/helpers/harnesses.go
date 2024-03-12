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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	"k8s.io/apimachinery/pkg/types"
)

// populateHarness describes a function that will insert suite-appropriate
// data into a MariaDBTestHarness instance
type populateHarness func(*MariaDBTestHarness)

// establishesCR describes a test function that can fully set up a particular
// controller's "Reconciliation Successful" state for a given kind of CR.
type establishesCR func(types.NamespacedName)

// updatesAccountName describes a test function that can change the
// "databaseAccount" or similar member of an already-reconciled CR to a new
// one, which is expected to kick off a username/password rotation sequence.
type updatesAccountName func(types.NamespacedName)

// deletesCr describes a test function that will delete the CR that was
// created by an establishesCR function
type deletesCR func()

type assertsURL func(types.NamespacedName, string, string)

type getsConfigHash func() string

// MariaDBTestHarness describes the parameters for running a series
// of Ginkgo tests which exercise a controller's ability to correctly
// work with MariaDBDatabase / MariaDBAccount APIs.
type MariaDBTestHarness struct {
	description     string
	namespace       string
	databaseName    string
	finalizerName   string
	PopulateHarness populateHarness
	SetupCR         establishesCR
	UpdateAccount   updatesAccountName
	DeleteCR        deletesCR
	mariaDBHelper   *TestHelper
	timeout         time.Duration
	interval        time.Duration
}

func (harness *MariaDBTestHarness) Setup(
	description,
	namespace string,
	databaseName string,
	finalizerName string,
	mariadb *TestHelper,
	timeout time.Duration,
	interval time.Duration,
) {
	harness.description = description
	harness.namespace = namespace
	harness.databaseName = databaseName
	harness.finalizerName = finalizerName
	harness.mariaDBHelper = mariadb
	harness.timeout = timeout
	harness.interval = interval
}

// RunBasicSuite runs MariaDBAccount suite tests.  these are
// pre-packaged ginkgo tests that exercise standard account create / update
// patterns that should be common to all controllers that work with
// MariaDBDatabase and MariaDBAccount CRs.
func (harness *MariaDBTestHarness) RunBasicSuite() {

	When(fmt.Sprintf("The %s service is being configured to run", harness.description), func() {
		BeforeEach(func() {
			harness.init()
		})

		It("Uses a pre-existing MariaDBAccount and sets a finalizer", func() {

			mariaDBHelper, timeout, interval := harness.mariaDBHelper, harness.timeout, harness.interval

			k8sClient := mariaDBHelper.K8sClient

			accountName := types.NamespacedName{
				Name:      "some-mariadb-account",
				Namespace: harness.namespace,
			}

			// create MariaDBAccount first
			acc, accSecret := mariaDBHelper.CreateMariaDBAccountAndSecret(accountName, mariadbv1.MariaDBAccountSpec{})
			DeferCleanup(k8sClient.Delete, mariaDBHelper.Ctx, accSecret)
			DeferCleanup(k8sClient.Delete, mariaDBHelper.Ctx, acc)

			// then create the CR
			harness.SetupCR(accountName)

			mariaDBHelper.Logger.Info(fmt.Sprintf("Service should fully configure on MariaDBAccount %s", accountName))

			// now wait for the account to exist
			mariadbAccount := mariaDBHelper.GetMariaDBAccount(accountName)
			Expect(mariadbAccount.Spec.UserName).ShouldNot(Equal(""))
			Expect(mariadbAccount.Spec.Secret).ShouldNot(Equal(""))
			mariaDBSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: mariadbAccount.Spec.Secret, Namespace: mariadbAccount.Namespace})
			Expect(string(mariaDBSecret.Data[mariadbv1.DatabasePasswordSelector])).ShouldNot(Equal(""))

			// wait for finalizer to be present
			Eventually(func() []string {
				mariadbAccount := mariaDBHelper.GetMariaDBAccount(accountName)
				return mariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: mariadbAccount.Spec.Secret, Namespace: mariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			// mariaDBDatabaseName is set
			Expect(mariadbAccount.Labels["mariaDBDatabaseName"]).Should(Equal(harness.databaseName))

		})

		It("Ensures a MariaDBAccount is created if not present and sets a finalizer", func() {
			mariaDBHelper, timeout, interval := harness.mariaDBHelper, harness.timeout, harness.interval

			accountName := types.NamespacedName{
				Name:      "some-mariadb-account",
				Namespace: harness.namespace,
			}

			// here, dont create a mariadbaccount.  right now CRs should
			// generate this if not exists using EnsureMariaDBAccount

			// then create the CR
			harness.SetupCR(accountName)

			mariaDBHelper.Logger.Info(fmt.Sprintf("Service should fully configure on MariaDBAccount %s", accountName))

			// now wait for the account to have the finalizer and the
			// database name
			// now wait for the account to exist
			mariadbAccount := mariaDBHelper.GetMariaDBAccount(accountName)
			Expect(mariadbAccount.Spec.UserName).ShouldNot(Equal(""))
			Expect(mariadbAccount.Spec.Secret).ShouldNot(Equal(""))
			mariaDBSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: mariadbAccount.Spec.Secret, Namespace: mariadbAccount.Namespace})
			Expect(string(mariaDBSecret.Data[mariadbv1.DatabasePasswordSelector])).ShouldNot(Equal(""))

			// wait for finalizer to be present
			Eventually(func() []string {
				mariadbAccount := mariaDBHelper.GetMariaDBAccount(accountName)
				return mariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: mariadbAccount.Spec.Secret, Namespace: mariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			// mariaDBDatabaseName is set
			Expect(mariadbAccount.Labels["mariaDBDatabaseName"]).Should(Equal(harness.databaseName))

		})
	})

	When(fmt.Sprintf("The %s service is fully running", harness.description), func() {
		BeforeEach(func() {
			harness.init()
		})

		// get service fully complete with a mariadbaccount
		BeforeEach(func() {
			mariaDBHelper, timeout, interval := harness.mariaDBHelper, harness.timeout, harness.interval

			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			// create the CR with old account
			harness.SetupCR(oldAccountName)

			// also simulate that it got completed
			mariaDBHelper.SimulateMariaDBAccountCompleted(oldAccountName)

			mariaDBHelper.Logger.Info(fmt.Sprintf("Service should fully configure on MariaDBAccount %s", oldAccountName))

			// finalizer is attached to old account
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				return oldMariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: oldMariadbAccount.Spec.Secret, Namespace: oldMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

		})
		It("should ensure a new MariaDBAccount exists when accountname is changed", func() {
			mariaDBHelper, timeout, interval := harness.mariaDBHelper, harness.timeout, harness.interval

			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			newAccountName := types.NamespacedName{
				Name:      "some-new-account",
				Namespace: harness.namespace,
			}

			mariaDBHelper.Logger.Info("About to update account from some-old-account to some-new-account")

			harness.UpdateAccount(newAccountName)

			// new account is (eventually) created
			_ = mariaDBHelper.GetMariaDBAccount(newAccountName)

			// dont simuluate MariaDBAccount being created. it's not done yet

			mariaDBHelper.Logger.Info(
				fmt.Sprintf("Service should have ensured MariaDBAccount %s exists but should remain running on %s",
					newAccountName, oldAccountName),
			)

			// finalizer is attached to new account
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				return newMariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: newMariadbAccount.Spec.Secret, Namespace: newMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			// old account retains the finalizer because we did not yet
			// complete the new MariaDBAccount
			Consistently(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				return oldMariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: oldMariadbAccount.Spec.Secret, Namespace: oldMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

		})

		It("should move the finalizer to a new MariaDBAccount when create is complete", func() {
			mariaDBHelper, timeout, interval := harness.mariaDBHelper, harness.timeout, harness.interval

			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			newAccountName := types.NamespacedName{
				Name:      "some-new-account",
				Namespace: harness.namespace,
			}

			harness.UpdateAccount(newAccountName)
			harness.mariaDBHelper.SimulateMariaDBAccountCompleted(newAccountName)

			mariaDBHelper.Logger.Info(
				fmt.Sprintf("Service should move to run fully off MariaDBAccount %s and remove finalizer from %s",
					newAccountName, oldAccountName),
			)

			// finalizer is attached to new account
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				return newMariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: newMariadbAccount.Spec.Secret, Namespace: newMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			// finalizer removed from old account
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				return oldMariadbAccount.Finalizers
			}, timeout, interval).ShouldNot(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: oldMariadbAccount.Spec.Secret, Namespace: oldMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).ShouldNot(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			// CreateOrPatchDBByName will add a label referring to the database
			Eventually(func() string {
				mariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				return mariadbAccount.Labels["mariaDBDatabaseName"]
			}, timeout, interval).Should(Equal(harness.databaseName))

		})

		It("should remove the finalizer from all associated MariaDBAccount objects regardless of status when deleted", func() {
			mariaDBHelper, timeout, interval := harness.mariaDBHelper, harness.timeout, harness.interval

			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			newAccountName := types.NamespacedName{
				Name:      "some-new-account",
				Namespace: harness.namespace,
			}

			mariaDBHelper.Logger.Info("About to update account from some-old-account to some-new-account")

			harness.UpdateAccount(newAccountName)

			// new account is (eventually) created
			_ = mariaDBHelper.GetMariaDBAccount(newAccountName)

			// dont simuluate MariaDBAccount being created, so that finalizer is
			// on both

			mariaDBHelper.Logger.Info(
				fmt.Sprintf("Service should have ensured MariaDBAccount %s exists but should remain running on %s",
					newAccountName, oldAccountName),
			)

			// as before, both accounts have a finalizer
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				return newMariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: newMariadbAccount.Spec.Secret, Namespace: newMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				return oldMariadbAccount.Finalizers
			}, timeout, interval).Should(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: oldMariadbAccount.Spec.Secret, Namespace: oldMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).Should(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			// now delete the CR
			harness.DeleteCR()

			// finalizer is removed from both as part of the delete
			// process
			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				return newMariadbAccount.Finalizers
			}, timeout, interval).ShouldNot(ContainElement(harness.finalizerName))

			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				return oldMariadbAccount.Finalizers
			}, timeout, interval).ShouldNot(ContainElement(harness.finalizerName))

			// as well as in the secret
			Eventually(func() []string {
				oldMariadbAccount := mariaDBHelper.GetMariaDBAccount(oldAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: oldMariadbAccount.Spec.Secret, Namespace: oldMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).ShouldNot(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

			Eventually(func() []string {
				newMariadbAccount := mariaDBHelper.GetMariaDBAccount(newAccountName)
				dbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: newMariadbAccount.Spec.Secret, Namespace: newMariadbAccount.Namespace})
				return dbSecret.Finalizers
			}, timeout, interval).ShouldNot(ContainElement(fmt.Sprintf("mariadb.openstack.org/%s", harness.finalizerName)))

		})

	})

}

// RunURLAssertSuite asserts that a database URL is set up with the correct
// username and password, and that this is updated when the account changes.
// account change is detected via finalizer
func (harness *MariaDBTestHarness) RunURLAssertSuite(assertURL assertsURL) {
	When(fmt.Sprintf("The %s service is fully running", harness.description), func() {
		BeforeEach(func() {
			harness.init()
		})

		BeforeEach(func() {
			mariaDBHelper := harness.mariaDBHelper

			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			k8sClient := mariaDBHelper.K8sClient

			// create MariaDBAccount / secret ahead of time, to suit controllers
			// that dont directly do EnsureMariaDBAccount
			mariadbAccount, mariadbSecret := mariaDBHelper.CreateMariaDBAccountAndSecret(oldAccountName, mariadbv1.MariaDBAccountSpec{})
			DeferCleanup(k8sClient.Delete, mariaDBHelper.Ctx, mariadbSecret)
			DeferCleanup(k8sClient.Delete, mariaDBHelper.Ctx, mariadbAccount)

			// create the CR with old account
			harness.SetupCR(oldAccountName)

			// also simulate that it got completed
			mariaDBHelper.SimulateMariaDBAccountCompleted(oldAccountName)

		})
		It("Sets the correct database URL for the MariaDBAccount", func() {
			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			mariadbAccount := harness.mariaDBHelper.GetMariaDBAccount(oldAccountName)
			mariadbSecret := harness.mariaDBHelper.GetSecret(types.NamespacedName{Name: mariadbAccount.Spec.Secret, Namespace: mariadbAccount.Namespace})

			assertURL(
				oldAccountName,
				mariadbAccount.Spec.UserName,
				string(mariadbSecret.Data[mariadbv1.DatabasePasswordSelector]),
			)
		})

		It("Updates the database URL when the MariaDBAccount changes", func() {

			newAccountName := types.NamespacedName{
				Name:      "some-new-account",
				Namespace: harness.namespace,
			}

			mariaDBHelper := harness.mariaDBHelper

			k8sClient := mariaDBHelper.K8sClient

			// create MariaDBAccount / secret ahead of time, to suit controllers
			// that dont directly do EnsureMariaDBAccount
			mariadbAccount, mariadbSecret := mariaDBHelper.CreateMariaDBAccountAndSecret(newAccountName, mariadbv1.MariaDBAccountSpec{})
			DeferCleanup(k8sClient.Delete, mariaDBHelper.Ctx, mariadbSecret)
			DeferCleanup(k8sClient.Delete, mariaDBHelper.Ctx, mariadbAccount)

			harness.UpdateAccount(newAccountName)
			harness.mariaDBHelper.SimulateMariaDBAccountCompleted(newAccountName)

			assertURL(
				newAccountName,
				mariadbAccount.Spec.UserName,
				string(mariadbSecret.Data[mariadbv1.DatabasePasswordSelector]),
			)
		})

	})
}

// RunConfigHashSuite asserts that a new config hash is generated when
// the account changes, which will result in pods being re-deployed
func (harness *MariaDBTestHarness) RunConfigHashSuite(getConfigHash getsConfigHash) {
	When(fmt.Sprintf("The %s service is fully running", harness.description), func() {
		BeforeEach(func() {
			harness.init()
		})

		BeforeEach(func() {
			mariaDBHelper := harness.mariaDBHelper

			oldAccountName := types.NamespacedName{
				Name:      "some-old-account",
				Namespace: harness.namespace,
			}

			// create the CR with old account
			harness.SetupCR(oldAccountName)

			// also simulate that it got completed
			mariaDBHelper.SimulateMariaDBAccountCompleted(oldAccountName)
		})

		It("Gets a config hash when the MariaDBAccount is complete", func() {
			configHash := getConfigHash()
			Eventually(func(g Gomega) {
				g.Expect(configHash).NotTo(Equal(""))
			}).Should(Succeed())

		})

		It("Updates the config hash when the MariaDBAccount changes", func() {

			newAccountName := types.NamespacedName{
				Name:      "some-new-account",
				Namespace: harness.namespace,
			}

			oldConfigHash := getConfigHash()

			harness.UpdateAccount(newAccountName)
			harness.mariaDBHelper.SimulateMariaDBAccountCompleted(newAccountName)

			Eventually(func(g Gomega) {
				newConfigHash := getConfigHash()
				g.Expect(newConfigHash).NotTo(Equal(""))
				g.Expect(newConfigHash).NotTo(Equal(oldConfigHash))
			}).Should(Succeed())

		})

	})
}

func (harness *MariaDBTestHarness) init() {
	harness.PopulateHarness(harness)
}
