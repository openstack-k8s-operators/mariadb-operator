#!/bin/bash
set +eux

if [ -e /var/lib/mysql/mysql ]; then
    echo -e "Database already exists. Reuse it."
else
    echo -e "Creating new mariadb database."
    # we need the right perm on the persistent directory,
    # so use Kolla to set it up before bootstrapping the DB
    cat <<EOF >/var/lib/pod-config-data/galera.cnf
[mysqld]
bind_address=localhost
wsrep_provider=none
EOF
    sudo -E kolla_set_configs
    kolla_extend_start
fi

# Generate the mariadb configs from the templates, these will get
# copied by `kolla_start` when the pod's main container will start
PODNAME=$(hostname -f | cut -d. -f1,2)
PODIP=$(grep "${PODNAME}" /etc/hosts | cut -d$'\t' -f1)
cd /var/lib/config-data
for cfg in *.cnf.in; do
    if [ -s "${cfg}" ]; then
        echo "Generating config file from template ${cfg}"
        sed -e "s/{ PODNAME }/${PODNAME}/" -e "s/{ PODIP }/${PODIP}/" "/var/lib/config-data/${cfg}" > "/var/lib/pod-config-data/${cfg%.in}"
    fi
done
