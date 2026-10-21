package mariadb

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTLSProtocol indicates a profile that cannot be represented by MariaDB and socat.
var ErrTLSProtocol = errors.New("unsupported TLS protocol profile")

// TLSConfigOptions holds the Apache-style TLS profile values merged into
// template options by lib-common and converts them for MariaDB and socat.
type TLSConfigOptions struct {
	Protocol    string
	CipherSuite string
}

// NewTLSConfigOptions constructs the typed options after lib-common has merged
// profile defaults. Empty values leave the existing template fallbacks in use.
func NewTLSConfigOptions(protocol, cipherSuite string) TLSConfigOptions {
	return TLSConfigOptions{Protocol: protocol, CipherSuite: cipherSuite}
}

// Versions returns the profile's enabled protocols in MariaDB's syntax.
func (o TLSConfigOptions) Versions() (string, error) {
	enabled := map[string]bool{
		"SSLv2":   false,
		"SSLv3":   false,
		"TLSv1.0": false,
		"TLSv1.1": false,
		"TLSv1.2": false,
		"TLSv1.3": false,
	}
	for token := range strings.FieldsSeq(o.Protocol) {
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
// TLS 1.3 suites and key exchange groups follow the system OpenSSL policy.
func (o TLSConfigOptions) LegacyCiphers() string {
	var ciphers []string
	for cipher := range strings.SplitSeq(o.CipherSuite, ":") {
		if !strings.HasPrefix(cipher, "TLS_") {
			ciphers = append(ciphers, cipher)
		}
	}
	return strings.Join(ciphers, ":")
}
