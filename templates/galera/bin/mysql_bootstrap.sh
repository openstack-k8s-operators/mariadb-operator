#!/bin/bash
set +eu

init_error() {
    echo "Container initialization failed at $(caller)." >&2
}
trap init_error ERR

if [ -e /var/lib/mysql/mysql ]; then
    echo -e "Database already exists. Reuse it."
    # set up permissions of mounted directories before starting
    # galera or the sidecar logging container
    sudo -E kolla_set_configs
else
    echo -e "Creating new mariadb database."
    # we need the right perm on the persistent directory,
    # so use Kolla to set it up before bootstrapping the DB
    cat <<EOF >/var/lib/config-data/generated/galera.cnf
[mysqld]
bind_address=localhost
wsrep_provider=none
EOF
    sudo -E kolla_set_configs
    kolla_extend_start
fi

# Generate the mariadb configs from the templates, these will get
# copied by `kolla_start` when the pod's main container will start
if [ "$(sysctl -n crypto.fips_enabled)" == "1" ]; then
    echo FIPS enabled
    SSL_CIPHER='ECDHE-RSA-AES256-GCM-SHA384'
else
    SSL_CIPHER='AES128-SHA256'
fi
export SSL_CIPHER

PODNAME=$(hostname -f | cut -d. -f1,2)
PODIPV4=$(grep "${PODNAME}" /etc/hosts | grep -v ':' | cut -d$'\t' -f1)
PODIPV6=$(grep "${PODNAME}" /etc/hosts | grep ':' | cut -d$'\t' -f1)
if [[ "" = "${PODIPV6}" ]]; then
    PODIP="${PODIPV4}"
    IPSTACK="IPV4"
else
    PODIP="[::]"
    IPSTACK="IPV6"
fi
export PODNAME PODIP

# mariabackup: default credentials if no configuration was provided
: ${MARIABACKUP_USER=root}
: ${MARIABACKUP_PASSWORD=$DB_ROOT_PASSWORD}
export MARIABACKUP_USER MARIABACKUP_PASSWORD

cd /var/lib/config-data/default
for cfg in *.cnf.in; do
    if [ -s "${cfg}" ]; then
        echo "Generating config file from template ${cfg}, will use ${IPSTACK} listen address of ${PODIP}"
        # replace all occurrences of "{ xxx }" with their value from environment
        awk '{
patsplit($0,markers,/{ (PODNAME|PODIP|SSL_CIPHER|MARIABACKUP_USER|MARIABACKUP_PASSWORD) }/);
for(i in markers){ m=markers[i]; gsub(/\W/,"",m); gsub(markers[i], ENVIRON[m])};
print $0
}' "/var/lib/config-data/default/${cfg}" > "/var/lib/config-data/generated/${cfg%.in}"
    fi
done
