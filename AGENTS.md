# AGENTS.md - mariadb-operator

## Project overview

mariadb-operator is a Kubernetes operator that manages
[MariaDB](https://mariadb.org/) database infrastructure (Galera clustering,
database provisioning, account management, backup and restore) for OpenStack
on OpenShift/Kubernetes. It is part of the
[openstack-k8s-operators](https://github.com/openstack-k8s-operators) project.

Key domain concepts: **Galera cluster** (multi-master replication),
**database instances**, **database accounts** (user credentials),
**backup** (Galera cluster backup), **restore** (Galera cluster recovery),
**custom configurations**.
## Tech stack

| Layer | Technology |
|-------|------------|
| Language | Go (modules, multi-module workspace via `go.work`) |
| Scaffolding | [Kubebuilder v4](https://book.kubebuilder.io/) + [Operator SDK](https://sdk.operatorframework.io/) |
| CRD generation | controller-gen (DeepCopy, CRDs, RBAC, webhooks) |
| Config management | Kustomize |
| Packaging | OLM bundle |
| Testing | Ginkgo/Gomega + envtest (functional), Chainsaw |
| Linting | golangci-lint (`.golangci.yaml`) |
| CI | Zuul (`zuul.d/`), Prow (`.ci-operator.yaml`), GitHub Actions |

## Custom Resources

| Kind | Purpose |
|------|---------|
| `Galera` | Manages a MariaDB Galera cluster (multi-master replication). |
| `MariaDBDatabase` | Provisions a database within a Galera cluster. |
| `MariaDBAccount` | Manages database user accounts and credentials. |
| `GaleraBackup` | Manages Galera cluster backups. |
| `GaleraRestore` | Manages Galera cluster restore operations. |

The `Galera`, `MariaDBDatabase`, and `MariaDBAccount` CRs have defaulting and
validating admission webhooks.

## Directory structure

**Maintenance rule:** when directories are added, removed, or renamed, or when
their purpose changes, update this table to match.

| Directory | Contents |
|-----------|----------|
| `api/v1beta1/` | CRD types (`galera_types.go`, `mariadbdatabase_types.go`, `mariadbaccount_types.go`, `galerabackup_types.go`, `galerarestore_types.go`), conditions, webhook markers |
| `api/test/` | Test helpers (`TestHelper`) for use by other operators' tests |
| `cmd/` | `main.go` entry point |
| `internal/controller/` | Reconcilers: `galera_controller.go`, `mariadbdatabase_controller.go`, `mariadbaccount_controller.go`, `galerabackup_controller.go`, `galerarestore_controller.go` |
| `internal/mariadb/` | MariaDB resource builders (statefulset, service, volumes, config) |
| `internal/webhook/` | Webhook implementation |
| `templates/` | Shell scripts and config templates: `account.sh`, `database.sh`, `delete_account.sh`, `delete_database.sh`, `galera/`, `galerabackup/` |
| `config/crd,rbac,manager,webhook/` | Generated Kubernetes manifests (CRDs, RBAC, deployment, webhooks) |
| `config/samples/` | Example CRs (Kustomize overlays). Includes custom config, TLS, and cert-manager variants. |
| `test/functional/` | envtest-based Ginkgo/Gomega tests |
| `test/chainsaw/` | Chainsaw integration tests |
| `hack/` | Helper scripts (CRD schema checker, local webhook runner) |

## Build commands

After modifying Go code, always run: `make generate manifests fmt vet`.

## Code style guidelines

- Follow standard openstack-k8s-operators conventions and lib-common patterns.
- Use `lib-common` modules for conditions, endpoints, TLS, storage, and other
  cross-cutting concerns rather than re-implementing them.
- CRD types go in `api/v1beta1/`. Controller logic goes in
  `internal/controller/`. Resource-building helpers go in `internal/mariadb/`.
- Config templates and shell scripts are in `templates/` -- they are mounted at
  runtime via the `OPERATOR_TEMPLATES` environment variable.
- Webhook logic is split between the kubebuilder markers in `api/v1beta1/` and
  the implementation in `internal/webhook/`.
- Test helpers in `api/test/` are consumed by other operators for their
  functional tests.

## Testing

- Functional tests use the envtest framework with Ginkgo/Gomega and live in
  `test/functional/`.
- Chainsaw integration tests live in `test/chainsaw/`.
- Run all functional tests: `make test`.
- When adding a new field or feature, add corresponding test cases in
  `test/functional/` and update fixture data accordingly.

## Key dependencies

- [lib-common](https://github.com/openstack-k8s-operators/lib-common): shared modules for conditions, endpoints, TLS, secrets, etc.
- [infra-operator](https://github.com/openstack-k8s-operators/infra-operator): topology APIs.
- [dev-docs/developer.md](https://github.com/openstack-k8s-operators/dev-docs/blob/main/developer.md): developer guide and coding conventions.
