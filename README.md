# mariadb-operator

## TLS security profile

When TLS is enabled on a Galera node, configuration templates will inherit the
`openstack-ssl-profile` ConfigMap in the same namespace, published by
the openstack-operator through lib-common. `SSLCipherSuite` supplies OpenSSL cipher
names, and `SSLProtocol` supplies Apache-style protocol selections. For example:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: openstack-ssl-profile
data:
  SSLCipherSuite: TLS_AES_256_GCM_SHA384:TLS_AES_128_GCM_SHA256
  SSLProtocol: "-all +TLSv1.3"
  TLSGroups: X25519MLKEM768
```

The mariadb-operator translates the protocol list into MariaDB `tls-version` settings
for SQL connections and backup clients, and protocol bounds for SST's socat
transport. Invalid or noncontiguous protocol selections fail configuration
generation rather than silently relaxing the policy. Profile changes refresh
configuration hashes for the Galera StatefulSet and backup CronJob. Changes to
the replication TLS policy use a coordinated cluster restart, because peers
with incompatible policies cannot communicate during a rolling update.

The optional `TLSGroups` key supplies a colon-separated list of key-exchange
groups. The profile producer must publish this key to select PQC groups such
as `X25519MLKEM768`; TLS 1.3 cipher names alone do not select a PQC key exchange.
`MinTLSVersion` (for example, `VersionTLS13`) is also supported when
`SSLProtocol` is absent.

Missing or empty keys retain the existing template defaults, including the
runtime FIPS cipher selection for Galera replication. Explicit
`spec.customServiceConfig` is preserved in the final `zzz_galera_custom.cnf`
file. Explicit `tls_version` and `ssl_cipher` options in its standard server
groups (`mysqld`, `server`, `mariadb`, `mariadbd`, `client-server`, `galera`)
override the profile and are reflected in the shared TLS policy.
`spec.tlsGroups` overrides the inherited
key-exchange groups for a Galera and its backup and restore workloads:

```yaml
spec:
  tlsGroups:
    - X25519MLKEM768
    - X25519
```

Galera replication and `socat` use the profile's TLS 1.2 cipher names where
available; their cipher-list APIs cannot configure TLS 1.3 suites. For a
TLS 1.3-only list they use a legacy default cipher list while the shared
OpenSSL configuration enforces TLS 1.3 and the requested suites. SQL, Galera
replication, SST, backups and restores load this configuration using
`OPENSSL_CONF`. It includes the container's `/etc/pki/tls/openssl.cnf`, preserving
its providers, FIPS settings and unspecified crypto-policy defaults.

The container and its peers must support the selected groups. SST additionally
requires socat with the `openssl-groups` option. Unsupported groups or socat
options fail rather than silently falling back to classical key exchange.
