package mariadb_test

import (
	"testing"

	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	backup "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb/backup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestTLSProfileWorkloadWiring(t *testing.T) {
	for _, tc := range []struct {
		name, certificate, ca string
	}{
		{"disabled", "", ""},
		{"SQL only", "db-cert", ""},
		{"SQL and replication", "db-cert", "db-ca"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			galera := &mariadbv1.Galera{
				ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "test"},
				Spec: mariadbv1.GaleraSpec{GaleraSpecCore: mariadbv1.GaleraSpecCore{
					Replicas: ptr.To[int32](1), StorageRequest: "500M", TargetVersion: "10.11",
				}},
			}
			if tc.certificate != "" {
				galera.Spec.TLS = tls.SimpleService{
					GenericService: tls.GenericService{SecretName: ptr.To(tc.certificate)},
					Ca:             tls.Ca{CaBundleSecretName: tc.ca},
				}
			}
			sts, err := mariadb.StatefulSet(galera, "hash", nil)
			if err != nil {
				t.Fatal(err)
			}
			b := &mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "test"}}
			b.Spec.DatabaseInstance = galera.Name
			cron := backup.BackupCronJob(b, galera, "hash")
			restore := backup.RestorePod(&mariadbv1.GaleraRestore{}, b, cron, galera)
			for name, pod := range map[string]*corev1.PodSpec{
				"Galera":  &sts.Spec.Template.Spec,
				"backup":  &cron.Spec.JobTemplate.Spec.Template.Spec,
				"restore": &restore.Spec,
			} {
				t.Run(name, func(t *testing.T) {
					want := tc.certificate != ""
					containers := append([]corev1.Container{pod.Containers[0]}, pod.InitContainers...)
					for _, container := range containers {
						found := false
						for _, env := range container.Env {
							if env.Name == "OPENSSL_CONF" {
								found = true
								if env.Value != mariadb.TLSProfileConfigPath {
									t.Errorf("%s uses unexpected profile path %q", container.Name, env.Value)
								}
							}
						}
						if found != want {
							t.Errorf("%s profile enabled = %v, want %v", container.Name, found, want)
						}
						if name == "backup" {
							flags := map[string]bool{}
							for _, item := range container.Env {
								flags[item.Name] = item.Value == "true"
							}
							if flags["MYSQL_TLS"] != want || flags["TLS"] != (tc.ca != "") {
								t.Errorf("backup SQL and replication TLS flags do not match the Galera configuration: %v", flags)
							}
						}
					}
					mounted := false
					for _, volume := range pod.Volumes {
						if volume.ConfigMap == nil {
							continue
						}
						for _, item := range volume.ConfigMap.Items {
							if item.Key != mariadb.TLSProfileConfigFile {
								continue
							}
							for _, mount := range pod.Containers[0].VolumeMounts {
								if mount.Name == volume.Name && mount.MountPath == "/var/lib/config-data/default" {
									mounted = true
								}
							}
						}
					}
					if mounted != want {
						t.Errorf("profile mounted = %v, want %v", mounted, want)
					}
				})
			}
		})
	}
}
