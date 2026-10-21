package mariadb

import (
	"errors"
	"testing"
)

func TestTLSVersions(t *testing.T) {
	for _, tc := range []struct {
		profile, want string
	}{
		{"-all +TLSv1.3", "TLSv1.3"},
		{"all -SSLv2 -SSLv3 -TLSv1 -TLSv1.1", "TLSv1.2,TLSv1.3"},
		{"TLSv1.2 TLSv1.3", "TLSv1.2,TLSv1.3"},
		{"TLSv1.3 TLSv1.2", "TLSv1.2,TLSv1.3"},
		{"-all +TLSv1 +TLSv1.1", "TLSv1.0,TLSv1.1"},
		{"-all +TLSv1.0", "TLSv1.0"},
		{"TLSv1 -TLSv1.0 +TLSv1.2", "TLSv1.2"},
		{"all -all +TLSv1.3", "TLSv1.3"},
		{"TLSv1.3 -TLSv1.3 +TLSv1.2", "TLSv1.2"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			options := NewTLSConfigOptions(tc.profile, "")
			got, err := options.Versions()
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, profile := range []string{"", "-all", "TLSv1.4", "SSLv3", "SSLv2", "TLSv1 TLSv1.3"} {
		t.Run("reject "+profile, func(t *testing.T) {
			options := NewTLSConfigOptions(profile, "")
			if _, err := options.Versions(); !errors.Is(err, ErrTLSProtocol) {
				t.Fatalf("expected invalid protocol error, got %v", err)
			}
			if _, err := options.SocketOptions(); !errors.Is(err, ErrTLSProtocol) {
				t.Fatalf("expected invalid socket protocol error, got %v", err)
			}
		})
	}
}

func TestTLSSocketOptions(t *testing.T) {
	for _, tc := range []struct{ profile, want string }{
		{"-all +TLSv1.3", "min-proto-version=TLS1.3,max-proto-version=TLS1.3"},
		{"TLSv1.2 TLSv1.3", "min-proto-version=TLS1.2,max-proto-version=TLS1.3"},
	} {
		options := NewTLSConfigOptions(tc.profile, "")
		got, err := options.SocketOptions()
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.profile, got, err, tc.want)
		}
	}
}

func TestTLSLegacyCiphers(t *testing.T) {
	for _, tc := range []struct{ suites, want string }{
		{"TLS_AES_256_GCM_SHA384", ""},
		{"TLS_AES_256_GCM_SHA384:ECDHE-RSA-AES256-GCM-SHA384:TLS_AES_128_GCM_SHA256", "ECDHE-RSA-AES256-GCM-SHA384"},
		{"ECDHE+AESGCM:!aNULL:!MD5", "ECDHE+AESGCM:!aNULL:!MD5"},
	} {
		options := NewTLSConfigOptions("", tc.suites)
		if got := options.LegacyCiphers(); got != tc.want {
			t.Errorf("%s: got %q; want %q", tc.suites, got, tc.want)
		}
	}
}
