# mariadb-operator update playbook

This ansible playbooks automates the testing of mariadb-operator updates,
including tests which disrupt the rolling update of Galera clusters:

  - Deploy the mariadb-operator from a container image
  - Deploy a Galera cluster (3-node by default)
  - Update the mariadb-operator to a new container image
  - If configured, trigger a disruption during the galera rolling update (default)
  - Verify that the update finished succesfully


## Prerequisites

1. **OpenShift/CRC Environment** up and running
4. **install_yamls** cloned and accessible
2. **ansible**, **oc**, **jq** available

## How to run this playbook

### Full end-to-end run: deploy operator -> galera CR -> update operator with pod disruption

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml
```

### Only deploy operator -> galera CR

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml --tags deploy,operator,galera
```

### Only update operator with pod disruption -> verify galera is restarted correctly

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml --tags update
```

### Only update part without pod disruption

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml --tags update -e disruption=false
```

### Full end-to-end run with 1-node Galera

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml -e replicas=1
```

### Full end-to-end run with custom operator images

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml \
                 -e mariadb_operator_image_initial=quay.io/openstack-k8s-operators/mariadb-operator-index:18.0-fr4-latest \
                 -e mariadb_operator_image_updated=quay.io/openstack-k8s-operators/mariadb-operator-index:18.0-fr5-latest
```

### Clean up OCP environment after a run

```bash
ansible-playbook -v playbooks/mariadb-operator-update.yaml --tags cleanup
```

## Customize playbook

Important variables from `./ansible/vars/mariadb-galera-vars.yaml`

| Variable | Default | Description |
|----------|---------|------------|-
| `tmp_dir` | `$PWD/out` | Location of temporary files |
| `install_yamls_dir` | `{{ playbook_dir }}/../../../install_yamls` | Location of the `install_yamls` repo |
| `mariadb_operator_image_initial` | `quay.io/.../mariadb-operator-index:latest` | Initial operator image |
| `mariadb_operator_image_updated` | `quay.io/.../mariadb-operator-index:latest` | Target operator image for update |
| `galera_cr` | `{{ tmp_dir }}/.../mariadb_v1beta1_galera.yaml` | YAML resource to instantiate the Galera CR |
| `replicas` | `3` | Number of Galera nodes |
| `namespace` | `openstack` | OpenStack namespace |
| `operator_namespace` | `openstack-operators` | Operator namespace |
| `deployment_timeout` | `120s` | Deployment timeout |
| `update_timeout` | `420s` | Update timeout |
| `disruption` | `true` | Stop pod 0 and pod 1 while pod 2 is restarting to force a Galera bootstrap |
