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
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
)

// MariaDB Condition Types used by API objects.
const (
	// MariaDBInitializedCondition Status=True condition which indicates if the MariaDB dbinit has completed
	MariaDBInitializedCondition condition.Type = "MariaDBInitialized"

	MariaDBDatabaseReadyCondition condition.Type = "MariaDBDatabaseReady"

	MariaDBAccountReadyCondition condition.Type = "MariaDBAccountReady"

	// MariaDBServerReadyCondition Status=True condition which indicates that the MariaDB and/or
	// Galera server is ready for database / account create/drop operations to proceed
	MariaDBServerReadyCondition condition.Type = "MariaDBServerReady"
)

// MariaDB Reasons used by API objects.
const (
	// ReasonDBError - DB error
	ReasonDBError condition.Reason = "DatabaseError"
	// ReasonDBPatchError - new resource set to reason Init
	ReasonDBPatchError condition.Reason = "DatabasePatchError"
	// ReasonDBPathOK - DB object created or patched ok
	ReasonDBPatchOK condition.Reason = "DatabasePatchOK"
	// ReasonDBNotFound - DB object not found
	ReasonDBNotFound condition.Reason = "DatabaseNotFound"
	// ReasonDBWaitingInitialized - waiting for service DB to be initialized
	ReasonDBWaitingInitialized condition.Reason = "DatabaseWaitingInitialized"
	// ReasonDBServiceNameError - error getting the DB service hostname
	ReasonDBServiceNameError condition.Reason = "DatabaseServiceNameError"

	// ReasonDBResourceDeleted - the galera resource has been marked for deletion
	ReasonDBResourceDeleted condition.Reason = "DatabaseResourceDeleted"

	// ReasonDBSync - Database sync in progress
	ReasonDBSync condition.Reason = "DBSync"
)

// MariaDB Messages used by API objects.
const (
	//
	// MariaDBReady condition messages
	//
	// MariaDBInitializedInitMessage
	MariaDBInitializedInitMessage = "MariaDB dbinit not started"

	// MariaDBInitializedReadyMessage
	MariaDBInitializedReadyMessage = "MariaDB dbinit completed"

	// MariaDBInitializedRunningMessage
	MariaDBInitializedRunningMessage = "MariaDB dbinit in progress"

	// MariaDBInitializedErrorMessage
	MariaDBInitializedErrorMessage = "MariaDB dbinit error occured %s"

	// MariaDBInputSecretNotFoundMessage
	MariaDBInputSecretNotFoundMessage = "Input secret not found: %s"

	MariaDBDatabaseReadyInitMessage = "MariaDBDatabase not yet available"

	MariaDBDatabaseReadyMessage = "MariaDBDatabase ready"

	MariaDBServerReadyInitMessage = "MariaDB / Galera server not yet available"

	MariaDBServerReadyMessage = "MariaDB / Galera server ready"

	MariaDBServerNotBootstrappedMessage = "MariaDB / Galera server not bootstrapped"

	MariaDBServerDeletedMessage = "MariaDB / Galera server has been marked for deletion"

	MariaDBAccountReadyInitMessage = "MariaDBAccount create / drop not started"

	MariaDBSystemAccountReadyMessage = "MariaDBAccount System account '%s' creation complete"

	MariaDBAccountReadyMessage = "MariaDBAccount creation complete"

	MariaDBAccountNotReadyMessage = "MariaDBAccount is not present: %s"

	MariaDBAccountSecretNotReadyMessage = "MariaDBAccount secret is missing or incomplete: %s"

	MariaDBErrorRetrievingMariaDBDatabaseMessage = "Error retrieving MariaDBDatabase instance %s"

	MariaDBErrorRetrievingMariaDBGaleraMessage = "Error retrieving MariaDB/Galera instance %s"

	MariaDBAccountFinalizersRemainMessage = "Waiting for finalizers %s to be removed before dropping username"

	MariaDBAccountReadyForDeleteMessage = "MariaDBAccount ready for delete"
)
