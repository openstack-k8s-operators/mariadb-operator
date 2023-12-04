package mariadb

import (
	"strings"

	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type accountCreateOrDeleteOptions struct {
	UserName              string
	DatabaseName          string
	DatabaseHostname      string
	DatabaseAdminUsername string
}

func CreateDbAccountJob(account *databasev1beta1.MariaDBAccount, databaseName string, databaseHostName string, databaseSecret string, containerImage string, serviceAccountName string) (*batchv1.Job, error) {

	opts := accountCreateOrDeleteOptions{account.Spec.UserName, databaseName, databaseHostName, "root"}
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
			Name:      strings.Replace(account.Spec.UserName, "_", "-", -1) + "-account-create",
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
												Name: account.Spec.Secret,
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

func DeleteDbAccountJob(account *databasev1beta1.MariaDBAccount, databaseName string, databaseHostName string, databaseSecret string, containerImage string, serviceAccountName string) (*batchv1.Job, error) {

	opts := accountCreateOrDeleteOptions{account.Spec.UserName, databaseName, databaseHostName, "root"}

	delCmd, err := util.ExecuteTemplateFile("delete_account.sh", &opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"owner": "mariadb-operator", "cr": account.Spec.UserName, "app": "mariadbschema",
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Replace(account.Spec.UserName, "_", "", -1) + "-account-delete",
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
							Name:    "mariadb-account-delete",
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
