package mariadb

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

// ErrTLSProtocol indicates a profile that cannot be represented by MariaDB and socat.
var ErrTLSProtocol = errors.New("unsupported TLS protocol profile")

// ErrTLSProfile indicates a profile value that cannot safely be rendered.
var ErrTLSProfile = errors.New("invalid TLS profile value")

const (
	// TLSProfileConfigFile holds OpenSSL defaults shared by SQL, Galera and SST.
	TLSProfileConfigFile = "openssl-profile.cnf"
	// TLSProfileConfigPath is the mounted OpenSSL profile configuration.
	TLSProfileConfigPath = "/var/lib/config-data/default/" + TLSProfileConfigFile
)

var (
	tlsCipherList = regexp.MustCompile(`^[A-Za-z0-9_:+!@=.,-]+$`)
	tlsGroupList  = regexp.MustCompile(`^[A-Za-z0-9_-]+(:[A-Za-z0-9_-]+)*$`)
)

// TLSConfigOptions holds the Apache-style TLS profile values merged into
// template options by lib-common and converts them for MariaDB and socat.
type TLSConfigOptions struct {
	Protocol    string
	CipherSuite string
	Groups      string
	MinVersion  string
}

// NewTLSConfigOptions constructs the typed options after lib-common has merged
// profile defaults. Explicit MariaDB server options override inherited values.
func NewTLSConfigOptions(values map[string]any) (TLSConfigOptions, error) {
	var options TLSConfigOptions
	for key, target := range map[string]*string{
		"SSLProtocol": &options.Protocol, "SSLCipherSuite": &options.CipherSuite,
		"TLSGroups": &options.Groups, "MinTLSVersion": &options.MinVersion,
	} {
		if raw, exists := values[key]; exists {
			value, ok := raw.(string)
			if !ok {
				return options, fmt.Errorf("%w: %s must be a string", ErrTLSProfile, key)
			}
			*target = value
		}
	}
	if custom, ok := values["customServiceConfig"].(string); ok {
		options = options.WithServiceConfig(custom)
	}
	if options.CipherSuite != "" && !tlsCipherList.MatchString(options.CipherSuite) {
		return options, fmt.Errorf("%w: SSLCipherSuite contains unsupported characters", ErrTLSProfile)
	}
	if options.Groups != "" && !tlsGroupList.MatchString(options.Groups) {
		return options, fmt.Errorf("%w: TLSGroups must be a colon-separated list of group names", ErrTLSProfile)
	}
	if options.Protocol != "" || options.MinVersion != "" {
		if _, err := options.Versions(); err != nil {
			return options, err
		}
	}
	return options, nil
}

// WithServiceConfig applies explicit TLS options from standard MariaDB server
// option groups. The original custom configuration is still mounted last.
func (o TLSConfigOptions) WithServiceConfig(config string) TLSConfigOptions {
	server := false
	for raw := range strings.SplitSeq(config, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			switch strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]"))) {
			case "mysqld", "server", "mariadb", "mariadbd", "client-server", "galera":
				server = true
			default:
				server = false
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !server || !found {
			continue
		}
		key = strings.ReplaceAll(strings.TrimSpace(key), "_", "-")
		key = strings.TrimPrefix(strings.ToLower(key), "loose-")
		value, _, _ = strings.Cut(value, "#")
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		switch key {
		case "tls-version":
			o.Protocol = "-all"
			for _, version := range strings.Split(value, ",") {
				o.Protocol += " +" + strings.TrimSpace(version)
			}
			o.MinVersion = ""
		case "ssl-cipher":
			o.CipherSuite = value
		}
	}
	return o
}

// Configured reports whether the profile supplies any TLS policy.
func (o TLSConfigOptions) Configured() bool {
	return o.Protocol != "" || o.MinVersion != "" || o.CipherSuite != "" || o.Groups != ""
}

// Versions returns the profile's enabled protocols in MariaDB's syntax.
func (o TLSConfigOptions) Versions() (string, error) {
	protocol := o.Protocol
	if o.MinVersion != "" {
		minimum, known := map[string]string{
			"VersionTLS10": "TLSv1.0", "VersionTLS11": "TLSv1.1",
			"VersionTLS12": "TLSv1.2", "VersionTLS13": "TLSv1.3",
		}[o.MinVersion]
		if !known {
			return "", fmt.Errorf("%w: unknown MinTLSVersion %q", ErrTLSProtocol, o.MinVersion)
		}
		if protocol == "" {
			var selected []string
			include := false
			for _, version := range []string{"TLSv1.0", "TLSv1.1", "TLSv1.2", "TLSv1.3"} {
				if version == minimum {
					include = true
				}
				if include {
					selected = append(selected, "+"+version)
				}
			}
			protocol = "-all " + strings.Join(selected, " ")
		}
	}
	enabled := map[string]bool{
		"SSLv2":   false,
		"SSLv3":   false,
		"TLSv1.0": false,
		"TLSv1.1": false,
		"TLSv1.2": false,
		"TLSv1.3": false,
	}
	for token := range strings.FieldsSeq(protocol) {
		on := !strings.HasPrefix(token, "-")
		name := strings.TrimPrefix(strings.TrimPrefix(token, "+"), "-")
		if name == "all" {
			for version := range enabled {
				enabled[version] = on
			}
			continue
		}
		if name == "TLSv1" {
			name = "TLSv1.0"
		}
		if _, known := enabled[name]; !known {
			return "", fmt.Errorf("%w: %q", ErrTLSProtocol, token)
		}
		enabled[name] = on
	}
	// MariaDB cannot enable SSLv2 or SSLv3. Excluding these in an Apache
	// profile is harmless, but enabling them must not silently change policy.
	if enabled["SSLv2"] || enabled["SSLv3"] {
		return "", fmt.Errorf("%w: SSLv2 and SSLv3 are not supported", ErrTLSProtocol)
	}
	var selected []string
	gap := false
	// Protocol order is explicit because map iteration is unordered and socat
	// requires the selected versions to form a contiguous range.
	for _, version := range []string{"TLSv1.0", "TLSv1.1", "TLSv1.2", "TLSv1.3"} {
		if enabled[version] {
			if gap {
				return "", fmt.Errorf("%w: socat requires a contiguous TLS version range", ErrTLSProtocol)
			}
			selected = append(selected, version)
		} else if len(selected) > 0 {
			gap = true
		}
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("%w: no TLS versions enabled", ErrTLSProtocol)
	}
	return strings.Join(selected, ","), nil
}

// SocketOptions returns socat's protocol bounds for the profile.
func (o TLSConfigOptions) SocketOptions() (string, error) {
	value, err := o.Versions()
	if err != nil {
		return "", err
	}
	versions := strings.Split(strings.ReplaceAll(value, "TLSv", "TLS"), ",")
	return "min-proto-version=" + versions[0] + ",max-proto-version=" + versions[len(versions)-1], nil
}

// LegacyCiphers returns the profile's TLS <= 1.2 cipher list.
// Galera and socat's cipher option use OpenSSL's TLS <= 1.2 cipher-list API.
// Passing only TLS 1.3 suites to that API fails with "no cipher match".
// TLS 1.3 suites and key exchange groups are applied through OpenSSLConfig.
func (o TLSConfigOptions) LegacyCiphers() string {
	var ciphers []string
	for cipher := range strings.SplitSeq(o.CipherSuite, ":") {
		if !strings.HasPrefix(cipher, "TLS_") {
			ciphers = append(ciphers, cipher)
		}
	}
	return strings.Join(ciphers, ":")
}

// CipherSuites returns the TLS 1.3 suites, whose OpenSSL API is separate from
// the legacy cipher list used by Galera and socat.
func (o TLSConfigOptions) CipherSuites() string {
	var suites []string
	for cipher := range strings.SplitSeq(o.CipherSuite, ":") {
		if strings.HasPrefix(cipher, "TLS_") {
			suites = append(suites, cipher)
		}
	}
	return strings.Join(suites, ":")
}

// OpenSSLConfig overlays the image's RHEL OpenSSL TLS policy. Including the
// system configuration preserves providers, FIPS properties, signature policy
// and all TLS defaults the profile leaves unspecified.
func (o TLSConfigOptions) OpenSSLConfig() (string, error) {
	config := ".include /etc/pki/tls/openssl.cnf\n"
	if !o.Configured() {
		return config, nil
	}
	config += "\n[default]\nconfig_diagnostics = 1\n" +
		"\n[openssl_init]\nssl_conf = ssl_module\n" +
		"\n[ssl_module]\nsystem_default = crypto_policy\n\n[crypto_policy]\n"
	tls13Only := o.CipherSuites() != "" && o.LegacyCiphers() == ""
	if o.Protocol != "" || o.MinVersion != "" || tls13Only {
		var versions string
		var err error
		if o.Protocol == "" && o.MinVersion == "" {
			versions = "TLSv1.3"
		} else {
			versions, err = o.Versions()
		}
		if err != nil {
			return "", err
		}
		list := strings.Split(versions, ",")
		minimum := strings.ReplaceAll(list[0], "TLSv1.0", "TLSv1")
		maximum := strings.ReplaceAll(list[len(list)-1], "TLSv1.0", "TLSv1")
		if tls13Only {
			if maximum != "TLSv1.3" {
				return "", fmt.Errorf("%w: TLS 1.3 ciphers require TLSv1.3", ErrTLSProfile)
			}
			minimum = "TLSv1.3"
		}
		if minimum == "TLSv1.3" && o.CipherSuite != "" && o.CipherSuites() == "" {
			return "", fmt.Errorf("%w: no cipher suites for TLSv1.3", ErrTLSProfile)
		}
		config += "TLS.MinProtocol = " + minimum + "\nTLS.MaxProtocol = " + maximum + "\n"
	}
	if o.CipherSuite != "" {
		config += "Ciphersuites = " + o.CipherSuites() + "\n"
		if legacy := o.LegacyCiphers(); legacy != "" {
			config += "CipherString = " + legacy + "\n"
		}
	}
	if o.Groups != "" {
		config += "Groups = " + o.Groups + "\n"
	}
	return config, nil
}

// TLSProfileEnv selects the mounted OpenSSL profile only for TLS-enabled pods.
func TLSProfileEnv(g *mariadbv1.Galera) []corev1.EnvVar {
	if !g.Spec.TLS.Enabled() {
		return nil
	}
	return []corev1.EnvVar{{Name: "OPENSSL_CONF", Value: TLSProfileConfigPath}}
}
