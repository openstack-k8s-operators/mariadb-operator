package mariadb

import (
	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// DbInitJob -
func DbInitJob(db *databasev1beta1.MariaDB, scheme *runtime.Scheme) (*batchv1.Job, error) {

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name + "-db-init",
			Namespace: db.Namespace,
			Labels:    GetLabels(db.Name),
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      "OnFailure",
					ServiceAccountName: "mariadb",
					Containers: []corev1.Container{
						{
							Name:  "mariadb-init",
							Image: db.Spec.ContainerImage,
							Env: []corev1.EnvVar{
								{
									Name:  "KOLLA_CONFIG_STRATEGY",
									Value: "COPY_ALWAYS",
								},
								{
									Name:  "KOLLA_BOOTSTRAP",
									Value: "true",
								},
								{
									Name:  "DB_MAX_TIMEOUT",
									Value: "60",
								},
								{
									Name: "DB_ROOT_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: db.Spec.Secret,
											},
											Key: "DbRootPassword",
										},
									},
								},
							},
							VolumeMounts: getInitVolumeMounts(),
						},
					},
					Volumes: getVolumes(db.Name),
				},
			},
		},
	}
	err := controllerutil.SetControllerReference(db, job, scheme)
	return job, err
}
