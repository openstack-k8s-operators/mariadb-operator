package mariadb

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// PodSecurityContext returns a PodSecurityContext with FSGroup set to the
// mysql UID. This ensures that EmptyDir and ConfigMap volumes are group-owned
// by the mysql user, which is required for pods that write to those volumes
// (e.g. galera pods writing to config-data-generated and var-local, or
// backup/restore pods writing to their working directories).
func PodSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		FSGroup: ptr.To(MysqlUID),
	}
}

// GaleraSecurityContext returns a SecurityContext for galera and related containers
func GaleraSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:                ptr.To(MysqlUID),
		RunAsGroup:               ptr.To(MysqlUID),
		RunAsNonRoot:             ptr.To(true),
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}
