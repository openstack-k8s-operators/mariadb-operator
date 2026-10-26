package mariadb

import (
	"errors"
	"strings"
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
			options := TLSConfigOptions{Protocol: tc.profile}
			got, err := options.Versions()
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, profile := range []string{"", "-all", "TLSv1.4", "SSLv3", "SSLv2", "TLSv1 TLSv1.3"} {
		t.Run("reject "+profile, func(t *testing.T) {
			options := TLSConfigOptions{Protocol: profile}
			if _, err := options.Versions(); !errors.Is(err, ErrTLSProtocol) {
				t.Fatalf("expected invalid protocol error, got %v", err)
			}
			if _, err := options.SocketOptions(); !errors.Is(err, ErrTLSProtocol) {
				t.Fatalf("expected invalid socket protocol error, got %v", err)
			}
		})
	}
}

func TestTLSProfileOptions(t *testing.T) {
	options, err := NewTLSConfigOptions(map[string]any{
		"MinTLSVersion": "VersionTLS13", "SSLCipherSuite": "TLS_AES_256_GCM_SHA384",
		"TLSGroups": "X25519MLKEM768",
	})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := options.Versions()
	if err != nil || versions != "TLSv1.3" {
		t.Fatalf("neutral minimum: got %q, %v", versions, err)
	}
	config, err := options.OpenSSLConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".include /etc/pki/tls/openssl.cnf", "config_diagnostics = 1", "TLS.MinProtocol = TLSv1.3", "TLS.MaxProtocol = TLSv1.3", "Ciphersuites = TLS_AES_256_GCM_SHA384", "Groups = X25519MLKEM768"} {
		if !strings.Contains(config, want) {
			t.Errorf("OpenSSL policy missing %q:\n%s", want, config)
		}
	}
}

func TestTLSProfileInvalidValues(t *testing.T) {
	for _, values := range []map[string]any{
		{"TLSGroups": true},
		{"TLSGroups": "X25519\nGroups = P-256"},
		{"TLSGroups": "?UnsupportedGroup:X25519"},
		{"SSLCipherSuite": "TLS_AES_256_GCM_SHA384\nGroups = X25519"},
		{"MinTLSVersion": "VersionTLS99"},
		{"SSLProtocol": "-all +TLSv1.4"},
	} {
		if _, err := NewTLSConfigOptions(values); err == nil {
			t.Errorf("accepted invalid profile: %v", values)
		}
	}
	for _, options := range []TLSConfigOptions{
		{Protocol: "TLSv1.2", CipherSuite: "TLS_AES_256_GCM_SHA384"},
		{Protocol: "TLSv1.3", CipherSuite: "ECDHE-RSA-AES256-GCM-SHA384"},
	} {
		if _, err := options.OpenSSLConfig(); !errors.Is(err, ErrTLSProfile) {
			t.Errorf("accepted incompatible protocols and ciphers: %+v, %v", options, err)
		}
	}
}

func TestTLSProfileDefaultsAndOverrides(t *testing.T) {
	config, err := (TLSConfigOptions{}).OpenSSLConfig()
	if err != nil || config != ".include /etc/pki/tls/openssl.cnf\n" {
		t.Fatalf("missing profile must retain system configuration: %q, %v", config, err)
	}
	for _, group := range []string{"mysqld", "server", "mariadb", "mariadbd", "client-server", "galera"} {
		options, err := NewTLSConfigOptions(map[string]any{
			"MinTLSVersion": "VersionTLS13", "SSLProtocol": "-all +TLSv1.3",
			"SSLCipherSuite": "TLS_AES_256_GCM_SHA384", "TLSGroups": "X25519",
			"customServiceConfig": "# ignored\n; ignored\n[client]\ntls_version=TLSv1.0\n[" + group + "]\nloose_tls_version = 'TLSv1.2' # override\nssl_cipher = ECDHE-RSA-AES256-GCM-SHA384\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		config, err := options.OpenSSLConfig()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(config, "TLS.MinProtocol = TLSv1.2\nTLS.MaxProtocol = TLSv1.2") ||
			!strings.Contains(config, "CipherString = ECDHE-RSA-AES256-GCM-SHA384") ||
			!strings.HasSuffix(config, "Groups = X25519\n") {
			t.Errorf("explicit service overrides were not applied last:\n%s", config)
		}
	}
	options, err := NewTLSConfigOptions(map[string]any{
		"SSLProtocol":         "-all +TLSv1.3",
		"customServiceConfig": "[mysqld]\ntls_version = TLSv1.2, TLSv1.3\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if versions, err := options.Versions(); err != nil || versions != "TLSv1.2,TLSv1.3" {
		t.Fatalf("comma-separated service protocols with spaces: got %q, %v", versions, err)
	}
}

func TestTLSProfileMinimumVersions(t *testing.T) {
	for _, tc := range []struct{ minimum, want string }{
		{"VersionTLS10", "TLSv1.0,TLSv1.1,TLSv1.2,TLSv1.3"},
		{"VersionTLS11", "TLSv1.1,TLSv1.2,TLSv1.3"},
		{"VersionTLS12", "TLSv1.2,TLSv1.3"},
		{"VersionTLS13", "TLSv1.3"},
	} {
		got, err := (TLSConfigOptions{MinVersion: tc.minimum}).Versions()
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.minimum, got, err, tc.want)
		}
	}
}

func TestTLSProfileCipherOnlyPolicy(t *testing.T) {
	options, err := NewTLSConfigOptions(map[string]any{"SSLCipherSuite": "TLS_AES_128_GCM_SHA256"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := options.OpenSSLConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, "TLS.MinProtocol = TLSv1.3\nTLS.MaxProtocol = TLSv1.3\n") {
		t.Fatalf("a TLS 1.3-only cipher list must prevent legacy cipher fallback:\n%s", config)
	}
	if _, err := (TLSConfigOptions{Protocol: "TLSv1.4"}).OpenSSLConfig(); !errors.Is(err, ErrTLSProtocol) {
		t.Fatalf("expected invalid protocol error, got %v", err)
	}
}

func TestTLSSocketOptions(t *testing.T) {
	for _, tc := range []struct{ profile, want string }{
		{"-all +TLSv1.3", "min-proto-version=TLS1.3,max-proto-version=TLS1.3"},
		{"TLSv1.2 TLSv1.3", "min-proto-version=TLS1.2,max-proto-version=TLS1.3"},
	} {
		options := TLSConfigOptions{Protocol: tc.profile}
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
		options := TLSConfigOptions{CipherSuite: tc.suites}
		if got := options.LegacyCiphers(); got != tc.want {
			t.Errorf("%s: got %q; want %q", tc.suites, got, tc.want)
		}
	}
}
