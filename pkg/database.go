package mariadb

import (
	"strings"

	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type dbCreateOptions struct {
	DatabaseName          string
	DatabaseHostname      string
	DatabaseAdminUsername string
}

// DbDatabaseJob -
func DbDatabaseJob(database *databasev1beta1.MariaDBDatabase, databaseHostName string, databaseSecret string, containerImage string) (*batchv1.Job, error) {

	opts := dbCreateOptions{database.Spec.Name, databaseHostName, "root"}
	dbCmd, err := util.ExecuteTemplateFile("database.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": database.Spec.Name, "app": "mariadbschema",
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// provided db name is used as metadata name where underscore is a not allowed
			// character. lets remove all underscored that underscores in the db name are
			// possible.
			Name:      strings.Replace(database.Spec.Name, "_", "", -1) + "-database-sync",
			Namespace: database.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      "OnFailure",
					ServiceAccountName: "mariadb-operator-mariadb",
					Containers: []corev1.Container{
						{
							Name:    "mariadb-database-create",
							Image:   containerImage,
							Command: []string{"/bin/sh", "-c", dbCmd},
							Env: []corev1.EnvVar{
								{
									Name: "MYSQL_PWD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: databaseSecret,
											},
											Key: "DbRootPassword",
										},
									},
								},
								{
									Name: "DatabasePassword",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: database.Spec.Secret,
											},
											Key: "DatabasePassword",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return job, nil
}

// DeleteDbDatabaseJob -
func DeleteDbDatabaseJob(database *databasev1beta1.MariaDBDatabase, databaseHostName string, databaseSecret string, containerImage string) (*batchv1.Job, error) {

	opts := dbCreateOptions{database.Spec.Name, databaseHostName, "root"}
	delCmd, err := util.ExecuteTemplateFile("delete_database.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": database.Spec.Name, "app": "mariadbschema",
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
					RestartPolicy:      "OnFailure",
					ServiceAccountName: "mariadb-operator-mariadb",
					Containers: []corev1.Container{
						{
							Name:    "mariadb-database-create",
							Image:   containerImage,
							Command: []string{"/bin/sh", "-c", delCmd},
							Env: []corev1.EnvVar{
								{
									Name: "MYSQL_PWD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: databaseSecret,
											},
											Key: "DbRootPassword",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return job, nil
}
