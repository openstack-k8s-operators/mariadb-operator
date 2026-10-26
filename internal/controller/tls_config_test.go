package controller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var errTLSProfileUnavailable = errors.New("profile unavailable")

func tlsTestClient(t *testing.T) client.Client {
	t.Helper()
	path, err := filepath.Abs("../../templates")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPERATOR_TEMPLATES", path)
	return tlsProfileTestClient(t)
}

func TestTLSProfileConfigMaps(t *testing.T) {
	for _, tc := range []struct {
		name string
		data map[string]string
	}{
		{"absent", nil},
		{"empty", map[string]string{}},
		{"cipher only", map[string]string{"SSLCipherSuite": "ECDHE-RSA-AES256-GCM-SHA384"}},
		{"protocol only", map[string]string{"SSLProtocol": "-all +TLSv1.3"}},
		{"modern", map[string]string{"SSLCipherSuite": "TLS_AES_256_GCM_SHA384", "SSLProtocol": "-all +TLSv1.3"}},
		{"PQC", map[string]string{"SSLCipherSuite": "TLS_AES_256_GCM_SHA384", "SSLProtocol": "-all +TLSv1.3", "TLSGroups": "X25519MLKEM768:X25519"}},
		{"mixed", map[string]string{"SSLCipherSuite": "TLS_AES_256_GCM_SHA384:ECDHE-RSA-AES256-GCM-SHA384", "SSLProtocol": "all -SSLv2 -SSLv3 -TLSv1 -TLSv1.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			c := tlsTestClient(t)
			if tc.data != nil {
				if err := c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: util.TLSProfileConfigMap, Namespace: "test"}, Data: tc.data}); err != nil {
					t.Fatal(err)
				}
			}
			galera := &mariadbv1.Galera{TypeMeta: metav1.TypeMeta{Kind: "Galera"}, ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "test", UID: "db"}}
			galera.Spec.CustomServiceConfig = "[mysqld]\nmax_connections = 123\n"
			backup := &mariadbv1.GaleraBackup{TypeMeta: metav1.TypeMeta{Kind: "GaleraBackup"}, ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "test", UID: "backup"}}
			for _, owner := range []client.Object{galera, backup} {
				h, err := helper.NewHelper(owner, c, nil, c.Scheme(), logr.Discard())
				if err != nil {
					t.Fatal(err)
				}
				hashes := map[string]env.Setter{}
				if owner == galera {
					err = (&GaleraReconciler{}).generateConfigMaps(ctx, h, galera, &hashes)
				} else {
					err = (&GaleraBackupReconciler{}).generateConfigMaps(ctx, h, backup, galera, &hashes)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(hashes) != 2 {
					t.Fatalf("expected script and config hashes, got %v", hashes)
				}
			}
			for _, name := range []string{"db-config-data", "backup-backup-config"} {
				cm := &corev1.ConfigMap{}
				if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: "test"}, cm); err != nil {
					t.Fatal(err)
				}
				if groups := tc.data["TLSGroups"]; groups != "" {
					if !strings.Contains(cm.Data["openssl-profile.cnf"], "Groups = "+groups+"\n") {
						t.Error("profile key-exchange groups missing from the OpenSSL configuration")
					}
					file := "galera_tls.cnf.in"
					if name == "backup-backup-config" {
						file = "mysql_backup_tls.cnf"
					}
					if !strings.Contains(cm.Data[file], ",openssl-groups="+groups+"\n") {
						t.Error("profile key-exchange groups missing from SST options")
					}
				}
				for file, rendered := range cm.Data {
					if file == mariadbv1.CustomServiceConfigFile {
						if rendered != galera.Spec.CustomServiceConfig {
							t.Fatal("custom configuration changed")
						}
						continue
					}
					if !strings.Contains(file, "tls") {
						continue
					}
					if cipher := tc.data["SSLCipherSuite"]; cipher != "" {
						want := "ssl-cipher = " + cipher + "\n"
						if strings.HasPrefix(file, "garbd") {
							want = "socket.ssl_cipher=ECDHE-RSA-AES256-GCM-SHA384;"
							if cipher == "TLS_AES_256_GCM_SHA384" {
								want = "socket.ssl_cipher=DEFAULT;"
							}
						}
						if !strings.Contains(rendered, want) {
							t.Errorf("%s missing %q:\n%s", file, want, rendered)
						}
					} else if file == "galera_tls.cnf.in" && !strings.Contains(rendered, "socket.ssl_cipher={ SSL_CIPHER };") {
						t.Error("lost the runtime FIPS cipher fallback")
					}
					if protocol := tc.data["SSLProtocol"]; protocol != "" && !strings.HasPrefix(file, "garbd") {
						want := "tls-version = TLSv1.3\n"
						if tc.name == "mixed" {
							want = "tls-version = TLSv1.2,TLSv1.3\n"
						}
						if !strings.Contains(rendered, want) {
							t.Errorf("%s missing %q:\n%s", file, want, rendered)
						}
						if file == "galera_tls.cnf.in" || file == "mysql_backup_tls.cnf" {
							minVersion := "TLS1.3"
							if tc.name == "mixed" {
								minVersion = "TLS1.2"
							}
							want = "min-proto-version=" + minVersion + ",max-proto-version=TLS1.3"
							if !strings.Contains(rendered, want) {
								t.Errorf("%s missing SST protocol bounds %q", file, want)
							}
						}
					} else if tc.data["SSLProtocol"] == "" && strings.Contains(rendered, "tls-version") {
						t.Errorf("%s unexpectedly limits protocols", file)
					}
					if strings.Contains(rendered, "<no value>") {
						t.Errorf("unresolved template in %s", file)
					}
				}
			}
		})
	}
}

func TestTLSProfileReadFailure(t *testing.T) {
	ctx := context.Background()
	c := tlsTestClient(t)
	c = interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == util.TLSProfileConfigMap {
				return errTLSProfileUnavailable
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	g := &mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "test"}}
	h, err := helper.NewHelper(g, c, nil, c.Scheme(), logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if err := (&GaleraReconciler{}).generateConfigMaps(ctx, h, g, nil); !errors.Is(err, errTLSProfileUnavailable) {
		t.Fatalf("expected profile read error, got %v", err)
	}
}
