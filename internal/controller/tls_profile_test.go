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
