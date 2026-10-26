package controller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var errTLSProfileList = errors.New("cannot list consumers")

func tlsProfileTestClient(t *testing.T, objects ...client.Object) client.WithWatch {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := mariadbv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestReplicationTLSProfileHash(t *testing.T) {
	ctx := context.Background()
	c := tlsProfileTestClient(t)
	reader := struct{ client.Reader }{Reader: c}
	galera := &mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "test"}}
	for _, enabled := range []bool{false, true} {
		if enabled {
			galera.Spec.TLS.SecretName = ptr.To("cert")
		}
		if hash, err := replicationTLSProfileHash(ctx, reader, galera); hash != "" || err != nil {
			t.Fatalf("TLS without encrypted replication should not require a cluster restart: %q, %v", hash, err)
		}
	}
	galera.Spec.TLS.CaBundleSecretName = "ca"
	if _, err := replicationTLSProfileHash(ctx, reader, galera); err == nil {
		t.Fatal("expected missing configuration to be reported")
	}
	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "db-config-data", Namespace: "test"},
		Data:       map[string]string{"openssl-profile.cnf": ".include /etc/pki/tls/openssl.cnf\n"},
	}
	if err := c.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	if hash, err := replicationTLSProfileHash(ctx, reader, galera); hash != "" || err != nil {
		t.Fatalf("system defaults should not add a profile restart requirement: %q, %v", hash, err)
	}
	var previous string
	for _, group := range []string{"X25519MLKEM768", "X25519"} {
		policy := "[crypto_policy]\nGroups = " + group + "\n"
		config.Data["openssl-profile.cnf"] = policy
		if err := c.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		hash, err := replicationTLSProfileHash(ctx, reader, galera)
		if err != nil {
			t.Fatal(err)
		}
		want, err := util.ObjectHash(policy)
		if err != nil {
			t.Fatal(err)
		}
		if hash != want || hash == previous {
			t.Fatalf("group-only changes must update the replication restart hash: %q", hash)
		}
		previous = hash
	}
}

func TestTLSProfileRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		list func() client.ObjectList
		want []string
	}{
		{"Galera", func() client.ObjectList { return &mariadbv1.GaleraList{} }, []string{"db-a", "db-b"}},
		{"GaleraBackup", func() client.ObjectList { return &mariadbv1.GaleraBackupList{} }, []string{"backup-a", "backup-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tlsProfileTestClient(t,
				&mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{Name: "db-a", Namespace: "test"}},
				&mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{Name: "db-b", Namespace: "test"}},
				&mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{Name: "outside", Namespace: "other"}},
				&mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "backup-a", Namespace: "test"}},
				&mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "backup-b", Namespace: "test"}},
				&mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "outside", Namespace: "other"}},
			)
			for _, namespace := range []string{"test", "empty"} {
				t.Run(namespace, func(t *testing.T) {
					source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: util.TLSProfileConfigMap, Namespace: namespace}}
					requests := tlsProfileRequests(context.Background(), c, source, tc.list())
					var names []string
					for _, request := range requests {
						if request.Namespace != namespace {
							t.Errorf("enqueued consumer from another namespace: %v", request)
						}
						names = append(names, request.Name)
					}
					slices.Sort(names)
					want := tc.want
					if namespace == "empty" {
						want = nil
					}
					if !slices.Equal(names, want) {
						t.Fatalf("enqueued %v; want %v", names, want)
					}
				})
			}
		})
	}
}

func TestTLSProfileRequestsIgnoreUnrelatedConfigMap(t *testing.T) {
	c := interceptor.NewClient(tlsProfileTestClient(t), interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			t.Fatal("unrelated ConfigMap must not list consumers")
			return nil
		},
	})
	source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "test"}}
	if requests := tlsProfileRequests(context.Background(), c, source, &mariadbv1.GaleraList{}); len(requests) != 0 {
		t.Fatalf("unrelated ConfigMap enqueued consumers: %v", requests)
	}
}

func TestTLSProfileRequestsListFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		list client.ObjectList
	}{
		{"Galera", &mariadbv1.GaleraList{}},
		{"GaleraBackup", &mariadbv1.GaleraBackupList{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := interceptor.NewClient(tlsProfileTestClient(t), interceptor.Funcs{
				List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
					return errTLSProfileList
				},
			})
			var logged string
			logger := funcr.New(func(_, message string) { logged = message }, funcr.Options{})
			ctx := log.IntoContext(context.Background(), logger)
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: util.TLSProfileConfigMap, Namespace: "test"}}
			if requests := tlsProfileRequests(ctx, c, source, tc.list); len(requests) != 0 {
				t.Fatalf("expected no consumers on list error, got %v", requests)
			}
			if !strings.Contains(logged, "Unable to list TLS profile consumers") || !strings.Contains(logged, errTLSProfileList.Error()) {
				t.Fatalf("list failure was not logged: %s", logged)
			}
		})
	}
}

func TestTLSProfileRequestsMalformedList(t *testing.T) {
	for _, tc := range []struct {
		name string
		list client.ObjectList
	}{
		{"missing items", &metav1.Table{}},
		{"item without metadata", &metav1.List{Items: []runtime.RawExtension{{Object: &runtime.Unknown{}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Bypass the fake client's decoding to simulate a list or item that
			// cannot be traversed through Kubernetes' metadata interfaces.
			c := interceptor.NewClient(tlsProfileTestClient(t), interceptor.Funcs{
				List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return nil },
			})
			var logged string
			logger := funcr.New(func(_, message string) { logged = message }, funcr.Options{})
			ctx := log.IntoContext(context.Background(), logger)
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: util.TLSProfileConfigMap, Namespace: "test"}}
			if requests := tlsProfileRequests(ctx, c, source, tc.list); len(requests) != 0 {
				t.Fatalf("malformed list enqueued consumers: %v", requests)
			}
			if !strings.Contains(logged, "Unable to read TLS profile consumers") {
				t.Fatalf("metadata failure was not logged: %s", logged)
			}
		})
	}
}

func TestGaleraBackupRequests(t *testing.T) {
	c := tlsProfileTestClient(t,
		&mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "matching", Namespace: "test"}, Spec: mariadbv1.GaleraBackupSpec{DatabaseInstance: "db"}},
		&mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "different-db", Namespace: "test"}, Spec: mariadbv1.GaleraBackupSpec{DatabaseInstance: "other"}},
		&mariadbv1.GaleraBackup{ObjectMeta: metav1.ObjectMeta{Name: "different-namespace", Namespace: "other"}, Spec: mariadbv1.GaleraBackupSpec{DatabaseInstance: "db"}},
	)
	source := &mariadbv1.Galera{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "test"}}
	requests := galeraBackupRequests(context.Background(), c, source)
	if len(requests) != 1 || requests[0].Name != "matching" || requests[0].Namespace != "test" {
		t.Fatalf("expected only the referencing backup, got %v", requests)
	}
	c = interceptor.NewClient(c, interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errTLSProfileList
		},
	})
	var logged string
	ctx := log.IntoContext(context.Background(), funcr.New(func(_, message string) { logged = message }, funcr.Options{}))
	if requests := galeraBackupRequests(ctx, c, source); len(requests) != 0 {
		t.Fatalf("expected no backups on list error, got %v", requests)
	}
	if !strings.Contains(logged, "Unable to list Galera backup consumers") {
		t.Fatalf("list failure was not logged: %s", logged)
	}
}
