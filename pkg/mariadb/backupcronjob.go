package mariadb

import (
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// StatefulSet returns a StatefulSet object for the galera cluster
func BackupCronJob(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera, configHash string) *batchv1.CronJob {
	ls := StatefulSetLabels(g)
	// storageRequest := resource.MustParse(g.Spec.StorageRequest)
	name := g.Name + "-backup"
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: g.Namespace,
			Labels:    ls,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:          b.Spec.Schedule,
			ConcurrencyPolicy: "Forbid",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: *getBackupPodTemplate(b, g),
				},
			},
		},
	}
	return cronJob
}

func getBackupPodTemplate(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) *corev1.PodTemplateSpec {
	ls := StatefulSetLabels(g)
	name := g.Name + "-backup"
	svcName := g.Name + "-galera" // TODO use service name
	return &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: ls,
		},
		Spec: corev1.PodSpec{
			Hostname:           name,
			Subdomain:          svcName,
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			ServiceAccountName: b.RbacResourceName(),
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup: ptr.To[int64](42434),
			},
			InitContainers: []corev1.Container{},
			Containers: []corev1.Container{{
				Image: g.Spec.ContainerImage,
				Name:  "galera",
				// Command: []string{"/usr/bin/dumb-init", "--", "/usr/bin/sleep", "infinity"},
				Command: []string{"/usr/bin/dumb-init", "--", "/bin/bash", "/var/lib/backup-scripts/backup_galera.sh"},
				Env: []corev1.EnvVar{{
					Name:  "DB",
					Value: g.Name,
				}, {
					Name:  "SECRET",
					Value: g.Spec.Secret,
				}},
				Ports: []corev1.ContainerPort{{
					ContainerPort: 4567,
					Name:          "galera",
				}, {
					ContainerPort: 4444,
					Name:          "galera-sst",
				}},
				VolumeMounts: []corev1.VolumeMount{{
					MountPath: "/var/lib/backup-scripts",
					ReadOnly:  true,
					Name:      "backup-scripts",
				}, {
					MountPath: "/mysql",
					Name:      "backup-data",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "backup-scripts",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: g.Name + "-scripts",
						},
						Items: []corev1.KeyToPath{
							{
								Key:  "backup_galera.sh",
								Path: "backup_galera.sh",
							},
						},
					},
				},
			}, {
				Name: "backup-data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "mysql-db-backup",
					},
				},
			}},
		},
	}
}
