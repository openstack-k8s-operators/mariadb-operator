package mariadb

import (
	util "github.com/openstack-k8s-operators/lib-common/pkg/util"
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

func DbDatabaseJob(database *databasev1beta1.MariaDBDatabase, databaseHostName string, databaseSecret string, containerImage string) *batchv1.Job {

	opts := dbCreateOptions{database.Spec.Name, databaseHostName, "root"}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": database.Spec.Name, "app": "mariadbschema",
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      database.Spec.Name + "-database-sync",
			Namespace: database.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      "OnFailure",
					ServiceAccountName: "mariadb",
					Containers: []corev1.Container{
						{
							Name:    "mariadb-database-create",
							Image:   containerImage,
							Command: []string{"/bin/sh", "-c", util.ExecuteTemplateFile("database.sh", &opts)},
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

	return job
}
