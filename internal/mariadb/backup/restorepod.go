// Package mariadbbackup provides utilities for creating and managing MariaDB backup jobs and resources
package mariadbbackup

import (
	"github.com/openstack-k8s-operators/lib-common/modules/common/pod"
	"github.com/openstack-k8s-operators/lib-common/modules/users"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestorePod returns a Pod object for a galera restore CR
func RestorePod(restoreCR *mariadbv1.GaleraRestore, backupCR *mariadbv1.GaleraBackup, backupCronJob *batchv1.CronJob) *corev1.Pod {
	galeraCR := &mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{
		Name:      backupCR.Spec.DatabaseInstance,
		Namespace: backupCR.Namespace,
	}}

	ls := RestorePodLabels(restoreCR)
	prefixName := RestorePodName(restoreCR)

	environ := []corev1.EnvVar{{
		Name:  "DB",
		Value: backupCR.Spec.DatabaseInstance,
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
			Labels:    ls,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			ServiceAccountName: restoreCR.RbacResourceName(),
			SecurityContext:    pod.RestrictivePodSecurityContext(users.MysqlUID, users.MysqlGID),
			Containers: []corev1.Container{{
				Image:           backupPodSpec.Containers[0].Image,
				Name:            "restore",
				Command:         []string{"/usr/bin/dumb-init", "--", "sleep", "infinity"},
				Env:             environ,
				SecurityContext: pod.RestrictiveSecurityContext(users.MysqlUID, users.MysqlGID),
				VolumeMounts:    RestoreVolumeMounts(backupCR, galeraCR),
			}},
			Volumes: RestoreVolumes(backupCR, galeraCR),
		},
	}
	return pod
}
