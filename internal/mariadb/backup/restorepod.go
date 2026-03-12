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

	// The restore pod uses the same container image as the configured backup CR,
	// so it can run the same mysql CLI version. It uses RestoreVolumes/RestoreVolumeMounts
	// which exclude the transfer-data volume since restore only reads SQL dumps
	// from the backup PVC. This avoids scheduling conflicts with local storage
	// (LVMS/TopoLVM) where the transfer PVC could land on a different node.
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
				VolumeMounts: RestoreVolumeMounts(backupCR, galeraCR),
			}},
			Volumes: RestoreVolumes(backupCR, galeraCR),
		},
	}
	return pod
}
