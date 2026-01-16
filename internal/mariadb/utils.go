package mariadb

import (
	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetLabels -
func GetLabels(name string) map[string]string {
	return map[string]string{"owner": "mariadb-operator", "cr": "mariadb-" + name, "app": "mariadb"}
}

// ServiceLabels - labels for service, match statefulset labels
func ServiceLabels(database metav1.Object) map[string]string {
	return labels.GetLabels(database, "mariadb", map[string]string{
		"owner": "mariadb-operator",
		"cr":    "mariadb-" + database.GetName(),
		"app":   "mariadb",
	})
}

// LabelSelectors - labels for service, match statefulset and service should match
func LabelSelectors(database metav1.Object, dbType string) map[string]string {
	return map[string]string{
		"app": dbType,
		"cr":  dbType + "-" + database.GetName(),
	}
}

// StatefulSetLabels - labels for statefulset, match service labels
func StatefulSetLabels(database metav1.Object) map[string]string {
	name := database.GetName()
	return labels.GetLabels(database, "galera", map[string]string{
		"owner":            "mariadb-operator",
		"app":              "galera",
		"cr":               "galera-" + name,
		common.AppSelector: StatefulSetName(name),
	})
}

// StatefulSetName - statefulset name for a galera CR
func StatefulSetName(name string) string {
	return name + "-galera"
}

// ResourceName - subresource name from a galera CR
func ResourceName(name string) string {
	return name + "-galera"
}

// ScriptConfigMapName - nam of script configmap tied to a galera CR
func ScriptConfigMapName(name string) string {
	return name + "-scripts"
}
