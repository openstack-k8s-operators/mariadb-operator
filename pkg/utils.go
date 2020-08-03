package mariadb

func GetLabels(name string) map[string]string {
	return map[string]string{"owner": "mariadb-operator", "cr": name, "app": "mariadb"}
}
