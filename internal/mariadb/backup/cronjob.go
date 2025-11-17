// Package mariadbbackup provides utilities for creating and managing MariaDB backup jobs and resources
package mariadbbackup

import (
	"strconv"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// BackupCronJob returns a CronJob object for the galera backup
func BackupCronJob(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera, configHash string) *batchv1.CronJob {
	ls := mariadb.StatefulSetLabels(g)
	name := BackupCronJobName(b, g)
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
					Template: *getBackupPodTemplate(b, g, configHash),
				},
			},
		},
	}
	return cronJob
}

func getBackupPodTemplate(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera, configHash string) *corev1.PodTemplateSpec {
	ls := mariadb.StatefulSetLabels(g)
	prefixName := BackupCronJobName(b, g)
	svcName := g.Name + "-galera" // TODO use service name

	// Option: retention
	var retentionTime string
	if b.Spec.Retention != nil {
		retentionTime = strconv.Itoa(int(b.Spec.Retention.Minutes()))
	} else {
		retentionTime = ""
	}

	environ := []corev1.EnvVar{{
		Name:  "CR_CONFIG_HASH",
		Value: configHash,
	}, {
		Name:  "DB",
		Value: g.Name,
	}, {
		Name:  "KOLLA_CONFIG_STRATEGY",
		Value: "COPY_ALWAYS",
	}, {
		Name:  "SECRET",
		Value: g.Spec.Secret,
	}, {
		Name:  "RETENTION",
		Value: retentionTime,
	}}
	if g.Spec.TLS.Enabled() && g.Spec.TLS.CaBundleSecretName != "" {
		environ = append(environ, corev1.EnvVar{
			Name:  "TLS",
			Value: "true",
		})
	}

	return &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prefixName,
			Namespace: g.Namespace,
			Labels:    ls,
		},
		Spec: corev1.PodSpec{
			Hostname:           prefixName,
			Subdomain:          svcName,
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			ServiceAccountName: b.RbacResourceName(),
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup: ptr.To[int64](42434),
			},
			InitContainers: []corev1.Container{},
			Containers: []corev1.Container{{
				Image:   g.Spec.ContainerImage,
				Name:    "backup",
				Command: []string{"/usr/bin/dumb-init", "--", "/usr/local/bin/kolla_start"},
				Env:     environ,
				Ports: []corev1.ContainerPort{{
					ContainerPort: 4567,
					Name:          "galera",
				}, {
					ContainerPort: 4444,
					Name:          "galera-sst",
				}},
				VolumeMounts: BackupVolumeMounts(b, g),
			}},
			Volumes: BackupVolumes(b, g),
		},
	}
}
