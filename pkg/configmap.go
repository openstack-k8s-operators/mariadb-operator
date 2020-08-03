package mariadb

import (
	util "github.com/openstack-k8s-operators/lib-common/pkg/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mariaDBConfigOptions struct {
	RootPassword string
	DBMaxTimeout string
}

func ConfigMap(cr *databasev1beta1.MariaDB, cmName string) *corev1.ConfigMap {
	opts := mariaDBConfigOptions{cr.Spec.RootPassword, cr.Spec.DBMaxTimeout}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: cr.Namespace,
		},
		Data: map[string]string{
			"mariadb_init.sh":  util.ExecuteTemplateFile("mariadb_init.sh", &opts),
			"galera.cnf":       util.ExecuteTemplateFile("galera.cnf", nil),
			"config.json":      util.ExecuteTemplateFile("config.json", nil),
			"init_config.json": util.ExecuteTemplateFile("init_config.json", nil),
		},
	}

	return cm
}
