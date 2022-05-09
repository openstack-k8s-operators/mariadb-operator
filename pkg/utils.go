package mariadb

// GetLabels -
func GetLabels(name string) map[string]string {
	return map[string]string{"owner": "mariadb-operator", "cr": "mariadb-" + name, "app": "mariadb"}
}
