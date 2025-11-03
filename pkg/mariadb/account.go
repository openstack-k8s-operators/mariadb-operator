// Package mariadb contains MariaDB database management functionality.
package mariadb

import (
	"strings"

	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type accountCreateOrDeleteOptions struct {
	UserName              string
	DatabaseName          string
	DatabaseHostname      string
	DatabaseAdminUsername string
	RequireTLS            string
}

// CreateDbAccountJob creates a Kubernetes job for creating a MariaDB database account
func CreateDbAccountJob(galera *mariadbv1.Galera, account *mariadbv1.MariaDBAccount, databaseName string, databaseHostName string, containerImage string, serviceAccountName string, nodeSelector *map[string]string) (*batchv1.Job, error) {
	var tlsStatement string
	if account.Spec.RequireTLS {
		tlsStatement = " REQUIRE SSL"
	} else {
		tlsStatement = ""
	}

	opts := accountCreateOrDeleteOptions{
		account.Spec.UserName,
		databaseName,
		databaseHostName,
		"root",
		tlsStatement,
	}
	dbCmd, err := util.ExecuteTemplateFile("account.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": account.Spec.UserName, "app": "mariadbschema",
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// provided db name is used as metadata name where underscore is a not allowed
			// character. Lets replace all underscores with hypen. Underscores in the db name are
			// possible.
			Name:      strings.ReplaceAll(account.Spec.UserName, "_", "-") + "-account-create",
			Namespace: account.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyOnFailure,
					ServiceAccountName: serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:    "mariadb-account-create",
							Image:   containerImage,
							Command: []string{"/bin/sh", "-c", dbCmd},
							Env: []corev1.EnvVar{
								{
									Name: "DatabasePassword",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: account.Spec.Secret,
											},
											Key: mariadbv1.DatabasePasswordSelector,
										},
									},
								},
							},
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

// DeleteDbAccountJob creates a Kubernetes job for deleting a MariaDB database account
func DeleteDbAccountJob(galera *mariadbv1.Galera, account *mariadbv1.MariaDBAccount, databaseName string, databaseHostName string, containerImage string, serviceAccountName string, nodeSelector *map[string]string) (*batchv1.Job, error) {

	opts := accountCreateOrDeleteOptions{account.Spec.UserName, databaseName, databaseHostName, "root", ""}

	delCmd, err := util.ExecuteTemplateFile("delete_account.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": account.Spec.UserName, "app": "mariadbschema",
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ReplaceAll(account.Spec.UserName, "_", "") + "-account-delete",
			Namespace: account.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyOnFailure,
					ServiceAccountName: serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:         "mariadb-account-delete",
							Image:        containerImage,
							Command:      []string{"/bin/sh", "-c", delCmd},
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
