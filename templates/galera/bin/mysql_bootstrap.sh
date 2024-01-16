#!/bin/bash
set -eu

init_error() {
    echo "Container initialization failed at $(caller)." >&2
}
trap init_error ERR

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
export PODNAME=$(hostname -f | cut -d. -f1-4)
export PODIP=$(grep "${PODNAME}" /etc/hosts | cut -d$'\t' -f1)
read -s -u 3 3< <(cat /var/lib/secrets/dbpassword; echo) DB_ROOT_PASSWORD
read -s -u 3 3< <(cat /var/lib/secrets/dbpassword; echo) MARIABACKUP_PASSWORD
export DB_ROOT_PASSWORD MARIABACKUP_PASSWORD

cd /var/lib/config-data
for cfg in *.cnf.in .*.cnf.in; do
    if [ -s "${cfg}" ]; then
        echo "Generating config file from template ${cfg}"
        awk '{
patsplit($0,vars,/{ (PODNAME|PODIP|DB_ROOT_PASSWORD|MARIABACKUP_PASSWORD) }/);
for(v in vars){ k=vars[v]; gsub(/\W/,"",k); gsub(vars[v], ENVIRON[k])};
print $0
}' "/var/lib/config-data/${cfg}" > "/var/lib/pod-config-data/${cfg%.in}"
    fi
done
