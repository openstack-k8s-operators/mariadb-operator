package mariadb

import (
	util "github.com/openstack-k8s-operators/lib-common/pkg/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type mariaDBConfigOptions struct {
	RootPassword string
	DBMaxTimeout int32
}

func ConfigMap(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) *corev1.ConfigMap {
	opts := mariaDBConfigOptions{db.Spec.RootPassword, 60}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Data: map[string]string{
			"mariadb_init.sh":  util.ExecuteTemplateFile("mariadb_init.sh", &opts),
			"galera.cnf":       util.ExecuteTemplateFile("galera.cnf", nil),
			"config.json":      util.ExecuteTemplateFile("config.json", nil),
			"init_config.json": util.ExecuteTemplateFile("init_config.json", nil),
		},
	}
	controllerutil.SetControllerReference(db, cm, scheme)
	return cm
}
