/*
Copyright 2022 Red Hat

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
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// NewDatabase returns a partially-initialized Database struct.
// Deprecated; use NewDatabaseForAccount
func NewDatabase(
	databaseName string,
	databaseUser string,
	secret string,
	labels map[string]string,
) *Database {
	return &Database{
		databaseName: databaseName,
		databaseUser: databaseUser,
		secret:       secret,
		labels:       labels,
		name:         "",
		accountName:  "",
		namespace:    "",
	}
}

// NewDatabase returns a partially-initialized Database struct.
// Deprecated; use NewDatabaseForAccount
func NewDatabaseWithNamespace(
	databaseName string,
	databaseUser string,
	secret string,
	labels map[string]string,
	name string,
	namespace string,
) *Database {
	return &Database{
		databaseName: databaseName,
		databaseUser: databaseUser,
		secret:       secret,
		labels:       labels,
		name:         name,
		accountName:  "",
		namespace:    namespace,
	}
}

// NewDatabaseForAccount returns an initialized Database struct.
// the stucture has all pre-requisite fields filled in, however has not
// yet populated its object parameters .database and .account
func NewDatabaseForAccount(
	databaseInstanceName string,
	databaseName string,
	name string,
	accountName string,
	namespace string,
) *Database {
	return &Database{
		databaseName: databaseName,
		mariadbName:  databaseInstanceName,
		name:         name,
		accountName:  accountName,
		namespace:    namespace,
	}
}

// setDatabaseHostname - set the service name of the DB as the databaseHostname
// by looking up the Service via the name of the MariaDB CR which provides it.
func (d *Database) setDatabaseHostname(ctx context.Context, h *helper.Helper) error {

	if d.mariadbName == "" {
		return fmt.Errorf("MariaDB CR name mariadbName field is blank")
	}

	// When the MariaDB CR provides the Service it sets the "cr" label of the
	// Service to "mariadb-<name of the MariaDB CR>". So we use this label
	// to select the right Service. See:
	// https://github.com/openstack-k8s-operators/mariadb-operator/blob/5781b0cf1087d7d28fa285bd5c44689acba92183/pkg/service.go#L17
	// https://github.com/openstack-k8s-operators/mariadb-operator/blob/590ffdc5ad86fe653f9cd8a7102bb76dfe2e36d1/pkg/utils.go#L4
	selector := map[string]string{
		"app": "mariadb",
		"cr":  fmt.Sprintf("mariadb-%s", d.mariadbName),
	}

	// assert that Database has the correct namespace.   This code
	// previously used h.GetBeforeObject().GetNamespace() for the
	// namespace, so this assertion allows us to use d.namespace directly
	// as we know it is the same value.  We basically want to stop relying
	// on h.GetBeforeObject()
	if h.GetBeforeObject().GetNamespace() != d.namespace {
		return fmt.Errorf(
			"helper namespace does not match the Database namespace %s != %s",
			h.GetBeforeObject().GetNamespace(), d.namespace,
		)
	}

	serviceList, err := service.GetServicesListWithLabel(
		ctx,
		h,
		d.namespace,
		selector,
	)
	if err != nil || len(serviceList.Items) == 0 {
		return fmt.Errorf("error getting the DB service using label %v: %w",
			selector, err)
	}

	// We assume here that a MariaDB CR instance always creates a single
	// Service. If multiple DB services are used the they are managed via
	// separate MariaDB CRs.
	if len(serviceList.Items) > 1 {
		return util.WrapErrorForObject(
			fmt.Sprintf("more then one DB service found %d", len(serviceList.Items)),
			d.database,
			err,
		)
	}
	svc := serviceList.Items[0]

	d.databaseHostname = svc.GetName() + "." + svc.GetNamespace() + ".svc"
	h.GetLogger().Info(fmt.Sprintf("Applied new databasehostname %s to MariaDBDatabase %s", d.databaseHostname, d.name))

	return nil
}

// GetTLSSupport - returns the secret name holding the database connection and client config
func (d *Database) GetTLSSupport() bool {
	return d.tlsSupport
}

// GetDatabaseHostname - returns the DB hostname which host the DB
func (d *Database) GetDatabaseHostname() string {
	return d.databaseHostname
}

// GetDatabase - returns the MariaDBDatabase object, if one has been loaded.
// may be nil if CreateOrPatchAll or GetDatabaseByNameAndAccount
// have not been called
func (d *Database) GetDatabase() *MariaDBDatabase {
	return d.database
}

// GetAccount - returns the MariaDBAccount object, if one has been loaded.
// may be nil if CreateOrPatchAll or GetDatabaseByNameAndAccount
// have not been called
func (d *Database) GetAccount() *MariaDBAccount {
	return d.account
}

// GetSecret - returns the Secret associated with the MariaDBAccount object,
// if one has been loaded.
// may be nil if CreateOrPatchAll or GetDatabaseByNameAndAccount
// have not been called
func (d *Database) GetSecret() *corev1.Secret {
	return d.secretObj
}

// CreateOrPatchDB - create or patch the service DB instance
// Deprecated. Use CreateOrPatchAll() after calling
// NewDatabaseForAccount.
func (d *Database) CreateOrPatchDB(
	ctx context.Context,
	h *helper.Helper,
) (ctrl.Result, error) {
	return d.CreateOrPatchDBByName(ctx, h, "openstack")
}

// CreateOrPatchDBByName - create or patch the service DB instance
// Deprecated. Use CreateOrPatchAll() after calling
// NewDatabaseForAccount.
func (d *Database) CreateOrPatchDBByName(
	ctx context.Context,
	h *helper.Helper,
	name string,
) (ctrl.Result, error) {

	if d.mariadbName == "" {
		d.mariadbName = name
	} else if d.mariadbName != name {
		return ctrl.Result{}, fmt.Errorf(
			"the given mariadbname and name sent to CreateOrPatchDBByName "+
				"do not match; %s != %s.  Use CreateOrPatchAll() for new code",
			d.mariadbName,
			name,
		)
	}

	// legacy h.GetBeforeObject() stuff.  we'd like the Database object
	// to be given all correct information up front by the caller.
	if d.name == "" {
		d.name = h.GetBeforeObject().GetName()
	}
	if d.namespace == "" {
		d.namespace = h.GetBeforeObject().GetNamespace()
	}

	return d.CreateOrPatchAll(ctx, h)
}

// CreateOrPatchAll - create or patch the MariaDBDatabase and
// MariaDBAccount.
func (d *Database) CreateOrPatchAll(
	ctx context.Context,
	h *helper.Helper,
) (ctrl.Result, error) {

	if d.mariadbName == "" {
		return ctrl.Result{}, fmt.Errorf(
			"MariaDB CR name is not present",
		)
	}
	if d.name == "" {
		return ctrl.Result{}, fmt.Errorf(
			"MariaDBDatabase CR name is not present",
		)

	}
	if d.accountName == "" {
		// no accountName at all.  this indicates this Database came about
		// using either NewDatabase or NewDatabaseWithNamespace; both
		// legacy and both pass along a databaseUser and secret.

		// so for forwards compatibility,
		// make a name and a MariaDBAccount for it.  name it the same as
		// the MariaDBDatabase so we can get it back
		// again based on that name alone (also for backwards compatibility).

		h.GetLogger().Info(
			fmt.Sprintf(
				"Database object for MariaDBDatabase %s does not have a MariaDBAccount CR name configured. "+
					"Assuming legacy use of the API, will use the same name for the MariaDBAccount.", d.name,
			),
		)

		d.accountName = d.name
	}

	mariaDBDatabase := d.database
	if mariaDBDatabase == nil {
		// MariaDBDatabase not present; create one to be patched/created

		mariaDBDatabase = &MariaDBDatabase{
			ObjectMeta: metav1.ObjectMeta{
				Name:      d.name,
				Namespace: d.namespace,
			},
			Spec: MariaDBDatabaseSpec{
				// the DB name must not change, therefore specify it outside the mutate function
				Name: d.databaseName,
			},
		}
	}

	mariaDBAccount := d.account

	if mariaDBAccount == nil {
		// MariaDBAccount not present.

		mariaDBAccount = &MariaDBAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      d.accountName,
				Namespace: d.namespace,
			},
		}

		// databaseUser was given, this is from legacy mode.  populate it
		// into the account
		if d.databaseUser != "" {
			mariaDBAccount.Spec.UserName = d.databaseUser
		}

		// secret was given, this is also from legacy mode.  populate it
		// into the account.  note here that this is osp-secret, which has
		// many PW fields in it.  By setting it here, as was the case when
		// osp-secret was associated directly with MariaDBDatabase, the
		// mariadb-controller is going to use the DatabasePassword value
		// for the password, and **not** any of the controller-specific
		// passwords.
		if d.secret != "" {
			mariaDBAccount.Spec.Secret = d.secret
		}
	}

	// set the database hostname on the db instance
	err := d.setDatabaseHostname(ctx, h)
	if err != nil {
		return ctrl.Result{}, err
	}

	op, err := controllerutil.CreateOrPatch(ctx, h.GetClient(), mariaDBDatabase, func() error {
		mariaDBDatabase.Labels = util.MergeStringMaps(
			mariaDBDatabase.GetLabels(),
			d.labels,
			map[string]string{"dbName": d.mariadbName},
		)

		err := controllerutil.SetControllerReference(h.GetBeforeObject(), mariaDBDatabase, h.GetScheme())
		if err != nil {
			return err
		}

		// If the service object doesn't have our finalizer, add it.
		controllerutil.AddFinalizer(mariaDBDatabase, h.GetFinalizer())

		return nil
	})

	if d.databaseHostname == "" {
		return ctrl.Result{}, fmt.Errorf("Database hostname is blank")
	}
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, util.WrapErrorForObject(
			fmt.Sprintf("Error create or update DB object %s", mariaDBDatabase.Name),
			mariaDBDatabase,
			err,
		)
	}

	if op != controllerutil.OperationResultNone {
		util.LogForObject(h, fmt.Sprintf("MariaDBDatabase object %s created or patched", mariaDBDatabase.Name), mariaDBDatabase)
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	opAcc, errAcc := CreateOrPatchAccount(
		ctx, h, mariaDBAccount,
		map[string]string{
			"mariaDBDatabaseName": d.name,
		},
	)

	if errAcc != nil {
		return ctrl.Result{}, util.WrapErrorForObject(
			fmt.Sprintf("Error creating or updating MariaDBAccount object %s", mariaDBAccount.Name),
			mariaDBAccount,
			errAcc,
		)
	}

	if opAcc != controllerutil.OperationResultNone {
		util.LogForObject(h, fmt.Sprintf("MariaDBAccount object %s created or patched", mariaDBAccount.Name), mariaDBAccount)
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	err = d.loadDatabaseAndAccountCRs(ctx, h)
	if err != nil {
		return ctrl.Result{}, err
	}

	d.tlsSupport = mariaDBDatabase.Status.TLSSupport

	return ctrl.Result{}, nil
}

// WaitForDBCreatedWithTimeout - wait until the MariaDBDatabase and MariaDBAccounts are
// initialized and reports Status.Conditions.IsTrue(MariaDBDatabaseReadyCondition)
// and Status.Conditions.IsTrue(MariaDBAccountReadyCondition)
func (d *Database) WaitForDBCreatedWithTimeout(
	ctx context.Context,
	h *helper.Helper,
	requeueAfter time.Duration,
) (ctrl.Result, error) {

	err := d.loadDatabaseAndAccountCRs(ctx, h)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if k8s_errors.IsNotFound(err) {
		util.LogForObject(
			h,
			fmt.Sprintf(
				"MariaDBDatabase %s and/or MariaDBAccount %s not yet found",
				d.database.Name,
				d.accountName,
			),
			d.database,
		)

		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if !d.database.Status.Conditions.IsTrue(MariaDBDatabaseReadyCondition) {
		util.LogForObject(
			h,
			fmt.Sprintf("Waiting for MariaDBDatabase %s to be fully reconciled", d.database.Name),
			d.database,
		)

		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if d.account != nil && !d.account.Status.Conditions.IsTrue(MariaDBAccountReadyCondition) {
		util.LogForObject(
			h,
			fmt.Sprintf("Waiting for MariaDBAccount %s to be fully reconciled", d.account.Name),
			d.account,
		)

		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	return ctrl.Result{}, nil
}

// WaitForDBCreated - wait until the MariaDBDatabase is initialized and reports Status.Completed == true
// Deprecated, use WaitForDBCreatedWithTimeout instead
func (d *Database) WaitForDBCreated(
	ctx context.Context,
	h *helper.Helper,
) (ctrl.Result, error) {
	return d.WaitForDBCreatedWithTimeout(ctx, h, time.Second*5)
}

// loadDatabaseAndAccountCRs - populate Database.database and Database.account
func (d *Database) loadDatabaseAndAccountCRs(
	ctx context.Context,
	h *helper.Helper,
) error {
	mariaDBDatabase := &MariaDBDatabase{}
	name := d.name
	namespace := d.namespace

	if d.name == "" {
		return fmt.Errorf(
			"MariaDBDatabase CR name is not present",
		)
	}
	if d.namespace == "" {
		return fmt.Errorf(
			"MariaDBDatabase CR namespace is not present",
		)
	}

	err := h.GetClient().Get(
		ctx,
		types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		},
		mariaDBDatabase)

	if err != nil {
		if k8s_errors.IsNotFound(err) {
			return util.WrapErrorForObject(
				fmt.Sprintf("Failed to get %s database %s ", name, namespace),
				h.GetBeforeObject(),
				err,
			)
		}

		return util.WrapErrorForObject(
			fmt.Sprintf("DB error %s %s ", name, namespace),
			h.GetBeforeObject(),
			err,
		)
	}

	d.database = mariaDBDatabase
	d.tlsSupport = mariaDBDatabase.Status.TLSSupport
	d.mariadbName = mariaDBDatabase.Labels["dbName"]
	accountName := d.accountName

	legacyAccount := false

	if accountName == "" {
		// no account name, so this is a legacy lookup.  locate MariaDBAccount
		// based on the same name as that of the MariaDBDatabase
		accountName = d.name
		legacyAccount = true
	}

	mariaDBAccount, secretObj, err := GetAccountAndSecret(ctx, h, accountName, namespace)

	if err != nil {
		if legacyAccount && k8s_errors.IsNotFound(err) {
			// if account can't be found, log it, but don't quit, still
			// return the Database with MariaDBDatabase
			h.GetLogger().Info(
				fmt.Sprintf("Could not find account %s for Database named %s", accountName, namespace),
			)

			// note that d.account remains nil in this case
		} else {
			// only if not legacy account, or other kind of error, do we
			// bail out
			return util.WrapErrorForObject(
				fmt.Sprintf("account error %s %s ", accountName, namespace),
				h.GetBeforeObject(),
				err,
			)
		}

	} else {
		d.account = mariaDBAccount
		d.databaseUser = mariaDBAccount.Spec.UserName
		d.secret = mariaDBAccount.Spec.Secret
		d.secretObj = secretObj
	}

	return nil
}

// GetDatabaseByName returns a *Database object with specified name and namespace
// deprecated; this needs to have the account name given as well for it to work
// completely
func GetDatabaseByName(
	ctx context.Context,
	h *helper.Helper,
	name string,
) (*Database, error) {
	db := &Database{
		name:      name,
		namespace: h.GetBeforeObject().GetNamespace(),
	}

	// then querying the MariaDBDatabase and store it in db by calling
	if err := db.loadDatabaseAndAccountCRs(ctx, h); err != nil {
		return db, err
	}
	return db, nil
}

func GetDatabaseByNameAndAccount(
	ctx context.Context,
	h *helper.Helper,
	name string,
	accountName string,
	namespace string,
) (*Database, error) {
	db := &Database{
		name:        name,
		accountName: accountName,
		namespace:   namespace,
	}
	// then querying the MariaDBDatabase and store it in db by calling
	if err := db.loadDatabaseAndAccountCRs(ctx, h); err != nil {
		return db, err
	}
	return db, nil
}

// DeleteFinalizer deletes a finalizer by its object from both
// MariaDBDatabase as well as all associated MariaDBAccount objects.
// if the Database object does not refer to any named MariaDBAccount, this
// is assumed to be legacy and the MariaDBAccount record step is skipped.
// note however this is not expected as MariaDBAccount creation is included
// with all the create functions in this module.
func (d *Database) DeleteFinalizer(
	ctx context.Context,
	h *helper.Helper,
) error {

	if d.account != nil && controllerutil.RemoveFinalizer(d.account, h.GetFinalizer()) {
		err := h.GetClient().Update(ctx, d.account)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return err
		}
		util.LogForObject(h, fmt.Sprintf("Removed finalizer %s from MariaDBAccount object", h.GetFinalizer()), d.account)

		// also do a delete for "unused" MariaDBAccounts, associated with
		// this MariaDBDatabase.
		DeleteUnusedMariaDBAccountFinalizers(
			ctx, h, d.database.Name, d.account.Name, d.account.Namespace,
		)
	}
	if controllerutil.RemoveFinalizer(d.database, h.GetFinalizer()) {
		err := h.GetClient().Update(ctx, d.database)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return err
		}
		util.LogForObject(h, fmt.Sprintf("Removed finalizer %s from MariaDBDatabase %s", h.GetFinalizer(), d.database.Spec.Name), d.database)

		if d.account == nil {
			util.LogForObject(
				h,
				fmt.Sprintf(
					"Warning: No MariaDBAccount CR was included when finalizer was removed from MariaDBDatabase %s",
					d.database.Spec.Name,
				),
				d.database,
			)
		}
	}
	return nil
}

// GetDatabaseClientConfig returns my.cnf client config
func (d *Database) GetDatabaseClientConfig(s *tls.Service) string {
	conn := []string{}
	conn = append(conn, "[client]")

	if s != nil && d.GetTLSSupport() {
		if s.CertMount != nil && s.KeyMount != nil {
			conn = append(conn,
				fmt.Sprintf("ssl-cert=%s", *s.CertMount),
				fmt.Sprintf("ssl-key=%s", *s.KeyMount),
			)
		}

		// Default to the env global bundle if not specified via CaMount
		caPath := tls.DownstreamTLSCABundlePath
		if s.CaMount != nil {
			caPath = *s.CaMount
		}
		conn = append(conn, fmt.Sprintf("ssl-ca=%s", caPath))

		if len(conn) > 0 {
			conn = append(conn, "ssl=1")
		}
	} else {
		conn = append(conn, "ssl=0")
	}

	return strings.Join(conn, "\n")
}

// DeleteUnusedMariaDBAccountFinalizers searches for all MariaDBAccounts
// associated with the given MariaDBDatabase name and removes the finalizer for all
// of them except for the given named account.
func DeleteUnusedMariaDBAccountFinalizers(
	ctx context.Context,
	h *helper.Helper,
	mariaDBDatabaseName string,
	mariaDBAccountName string,
	namespace string,
) error {

	accountList := &MariaDBAccountList{}

	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{"mariaDBDatabaseName": mariaDBDatabaseName},
	}

	if err := h.GetClient().List(ctx, accountList, opts...); err != nil {
		h.GetLogger().Error(err, "Unable to retrieve MariaDBAccountList")
		return nil
	}

	for _, mariaDBAccount := range accountList.Items {

		if mariaDBAccount.Name == mariaDBAccountName {
			continue
		}

		if controllerutil.RemoveFinalizer(&mariaDBAccount, h.GetFinalizer()) {
			err := h.GetClient().Update(ctx, &mariaDBAccount)
			if err != nil && !k8s_errors.IsNotFound(err) {
				h.GetLogger().Error(err, fmt.Sprintf("Unable to remove finalizer %s from MariaDBAccount %s", h.GetFinalizer(), mariaDBAccount.Name))
				return err
			}
			util.LogForObject(h, fmt.Sprintf("Removed finalizer %s from MariaDBAccount", h.GetFinalizer()), &mariaDBAccount)
		}

	}
	return nil

}

// CreateOrPatchAccount creates/updates a given MariaDBAccount CR.
func CreateOrPatchAccount(
	ctx context.Context,
	h *helper.Helper,
	account *MariaDBAccount,
	labels map[string]string,
) (controllerutil.OperationResult, error) {
	opAcc, errAcc := controllerutil.CreateOrPatch(ctx, h.GetClient(), account, func() error {
		account.Labels = util.MergeStringMaps(
			account.GetLabels(),
			labels,
		)

		err := controllerutil.SetControllerReference(h.GetBeforeObject(), account, h.GetScheme())
		if err != nil {
			return err
		}

		// If the service object doesn't have our finalizer, add it.
		controllerutil.AddFinalizer(account, h.GetFinalizer())

		if account.Spec.UserName == "" {
			return fmt.Errorf("no UserName field in account %s", account.Name)
		}
		if account.Spec.Secret == "" {
			return fmt.Errorf("no secret field in account %s", account.Name)
		}

		return nil
	})

	return opAcc, errAcc
}

// GetAccount returns an existing MariaDBAccount object from the cluster
func GetAccount(ctx context.Context,
	h *helper.Helper,
	accountName string, namespace string,
) (*MariaDBAccount, error) {
	databaseAccount := &MariaDBAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      accountName,
			Namespace: namespace,
		},
	}
	objectKey := client.ObjectKeyFromObject(databaseAccount)

	err := h.GetClient().Get(ctx, objectKey, databaseAccount)
	if err != nil {
		return nil, err
	}
	return databaseAccount, err
}

// GetAccount returns an existing MariaDBAccount object and its associated
// Secret object from the cluster
func GetAccountAndSecret(ctx context.Context,
	h *helper.Helper,
	accountName string, namespace string,
) (*MariaDBAccount, *corev1.Secret, error) {

	databaseAccount, err := GetAccount(ctx, h, accountName, namespace)
	if err != nil {
		return nil, nil, err
	}

	if databaseAccount.Spec.Secret == "" {
		return nil, nil, fmt.Errorf("no secret field present in MariaDBAccount %s", accountName)
	}

	dbSecret, _, err := secret.GetSecret(ctx, h, databaseAccount.Spec.Secret, namespace)
	if err != nil {
		return nil, nil, err
	}

	return databaseAccount, dbSecret, nil
}

// EnsureMariaDBAccount ensures a MariaDBAccount has been created for a given
// operator calling the function, and returns the MariaDBAccount and its
// Secret for use in consumption into a configuration.
// The current version of the function creates the objects if they don't
// exist; a later version of this can be set to only ensure that the objects
// were already created by an external actor such as openstack-operator up
// front.
func EnsureMariaDBAccount(ctx context.Context,
	helper *helper.Helper,
	accountName string, namespace string, requireTLS bool,
	userNamePrefix string,
) (*MariaDBAccount, *corev1.Secret, error) {

	if accountName == "" {
		return nil, nil, fmt.Errorf("accountName is empty")
	}

	account, err := GetAccount(ctx, helper, accountName, namespace)

	if err != nil {
		if !k8s_errors.IsNotFound(err) {
			return nil, nil, err
		}

		username, err := generateUniqueUsername(userNamePrefix)
		if err != nil {
			return nil, nil, err
		}

		account = &MariaDBAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      accountName,
				Namespace: namespace,
				// note no labels yet; the account will not have a
				// mariadbdatabase yet so the controller will not
				// try to create a DB; it instead will respond again to the
				// MariaDBAccount once this is filled in
			},
			Spec: MariaDBAccountSpec{
				UserName:   username,
				Secret:     fmt.Sprintf("%s-db-secret", accountName),
				RequireTLS: requireTLS,
			},
		}

	} else {
		account.Spec.RequireTLS = requireTLS

		if account.Spec.Secret == "" {
			account.Spec.Secret = fmt.Sprintf("%s-db-secret", accountName)
		}
	}

	dbSecret, _, err := secret.GetSecret(ctx, helper, account.Spec.Secret, namespace)

	if err != nil {
		if !k8s_errors.IsNotFound(err) {
			return nil, nil, err
		}

		dbPassword, err := generateDBPassword()
		if err != nil {
			return nil, nil, err
		}

		dbSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Spec.Secret,
				Namespace: namespace,
			},
			StringData: map[string]string{
				DatabasePasswordSelector: dbPassword,
			},
		}

		_, _, errSecret := secret.CreateOrPatchSecret(
			ctx,
			helper,
			helper.GetBeforeObject(),
			dbSecret,
		)
		if errSecret != nil {
			return nil, nil, errSecret
		}

	}

	_, errAcc := CreateOrPatchAccount(ctx, helper, account, map[string]string{})
	if errAcc != nil {
		return nil, nil, errAcc
	}

	util.LogForObject(
		helper,
		fmt.Sprintf(
			"Successfully ensured MariaDBAccount %s exists; database username is %s",
			accountName,
			account.Spec.UserName,
		),
		account,
	)

	return account, dbSecret, nil
}

// generateUniqueUsername creates a MySQL-compliant database username based on
// a prefix and a 4 character hex string generated randomly
func generateUniqueUsername(usernamePrefix string) (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"%s_%s",
		strings.Replace(usernamePrefix, "-", "_", -1),
		hex.EncodeToString(b)), nil

}

// generateDBPassword produces a hex string from a cryptographically secure
// random number generator
func generateDBPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
