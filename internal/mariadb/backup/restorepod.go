// Package mariadbbackup provides utilities for creating and managing MariaDB backup jobs and resources
package mariadbbackup

import (
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// RestorePod returns a Pod object for a galera restore CR
func RestorePod(restoreCR *mariadbv1.GaleraRestore, backupCR *mariadbv1.GaleraBackup, backupCronJob *batchv1.CronJob) *corev1.Pod {
	galeraCR := &mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{
		Name:      backupCR.Spec.DatabaseInstance,
		Namespace: backupCR.Namespace,
	}}

	prefixName := RestorePodName(restoreCR, galeraCR)

	environ := []corev1.EnvVar{{
		Name:  "DB",
		Value: backupCR.Spec.DatabaseInstance,
	}, {
		Name:  "KOLLA_CONFIG_STRATEGY",
		Value: "COPY_ALWAYS",
	}}

	// The restore pod uses the same volume mounts and container image
	// as the configured backup CR, so it automatically has access to
	// the captured backups, and it can run the same mysql CLI version
	backupPodSpec := backupCronJob.Spec.JobTemplate.Spec.Template.Spec
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prefixName,
			Namespace: backupCR.Namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			ServiceAccountName: restoreCR.RbacResourceName(),
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup: ptr.To[int64](42434),
			},
			InitContainers: []corev1.Container{},
			Containers: []corev1.Container{{
				Image: backupPodSpec.Containers[0].Image,
				Name:  "restore",
				Command: []string{"/usr/bin/dumb-init", "--", "/bin/bash", "-c",
					"sudo -E /usr/local/bin/kolla_set_configs;" +
						"sudo -E /usr/local/bin/kolla_copy_cacerts;" +
						"sleep infinity"},
				Env:          environ,
				VolumeMounts: backupPodSpec.Containers[0].VolumeMounts,
			}},
			Volumes: backupPodSpec.Volumes,
		},
	}
	return pod
}
