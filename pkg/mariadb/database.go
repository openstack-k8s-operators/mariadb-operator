package mariadb

import (
	"strings"

	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type dbCreateOptions struct {
	DatabaseName          string
	DatabaseHostname      string
	DatabaseAdminUsername string
	DefaultCharacterSet   string
	DefaultCollation      string
	DatabaseUserTLS       string
}

// DbDatabaseJob -
func DbDatabaseJob(galera *mariadbv1.Galera, database *databasev1beta1.MariaDBDatabase, databaseHostName string, containerImage string, serviceAccountName string, useTLS bool, nodeSelector *map[string]string) (*batchv1.Job, error) {
	var tlsStatement string
	if useTLS {
		tlsStatement = " REQUIRE SSL"
	} else {
		tlsStatement = ""
	}

	opts := dbCreateOptions{
		database.Spec.Name,
		databaseHostName,
		"root",
		database.Spec.DefaultCharacterSet,
		database.Spec.DefaultCollation,
		tlsStatement,
	}
	dbCmd, err := util.ExecuteTemplateFile("database.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": database.Spec.Name, "app": "mariadbschema",
	}

	var scriptEnv []corev1.EnvVar

	if database.Spec.Secret != nil {
		scriptEnv = []corev1.EnvVar{
			// send deprecated Secret field but only if non-nil
			{
				Name: "DatabasePassword",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: *database.Spec.Secret,
						},
						Key: "DatabasePassword",
					},
				},
			},
		}
	} else {
		scriptEnv = []corev1.EnvVar{}
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// provided db name is used as metadata name where underscore is a not allowed
			// character. Lets replace all underscores with hypen. Underscores in the db name are
			// possible.
			Name:      strings.Replace(database.Spec.Name, "_", "-", -1) + "-db-create",
			Namespace: database.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyOnFailure,
					ServiceAccountName: serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:         "mariadb-database-create",
							Image:        containerImage,
							Command:      []string{"/bin/sh", "-c", dbCmd},
							Env:          scriptEnv,
							VolumeMounts: getGaleraRootOnlyVolumeMounts(),
						},
					},
					Volumes: getGaleraRootOnlyVolumes(galera),
				},
			},
		},
	}

	if nodeSelector != nil && len(*nodeSelector) > 0 {
		job.Spec.Template.Spec.NodeSelector = *nodeSelector
	}

	return job, nil
}

// DeleteDbDatabaseJob -
func DeleteDbDatabaseJob(galera *mariadbv1.Galera, database *databasev1beta1.MariaDBDatabase, databaseHostName string, containerImage string, serviceAccountName string, nodeSelector *map[string]string) (*batchv1.Job, error) {

	opts := dbCreateOptions{
		database.Spec.Name,
		databaseHostName,
		"root",
		database.Spec.DefaultCharacterSet,
		database.Spec.DefaultCollation,
		"",
	}
	delCmd, err := util.ExecuteTemplateFile("delete_database.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": database.Spec.Name, "app": "mariadbschema",
	}

	var scriptEnv []corev1.EnvVar

	if database.Spec.Secret != nil {
		scriptEnv = []corev1.EnvVar{
			// send deprecated Secret field but only if non-nil.  otherwise
			// the script should not try to drop usernames from mysql.user
			{
				Name: "DatabasePassword",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: *database.Spec.Secret,
						},
						Key: databasev1beta1.DatabasePasswordSelector,
					},
				},
			},
		}
	} else {
		scriptEnv = []corev1.EnvVar{}
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Replace(database.Spec.Name, "_", "", -1) + "-database-delete",
			Namespace: database.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyOnFailure,
					ServiceAccountName: serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:         "mariadb-database-create",
							Image:        containerImage,
							Command:      []string{"/bin/sh", "-c", delCmd},
							Env:          scriptEnv,
							VolumeMounts: getGaleraRootOnlyVolumeMounts(),
						},
					},
					Volumes: getGaleraRootOnlyVolumes(galera),
				},
			},
		},
	}

	if nodeSelector != nil {
		job.Spec.Template.Spec.NodeSelector = *nodeSelector
	}

	return job, nil
}
