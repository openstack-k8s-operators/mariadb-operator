#!/bin/bash
set +eux

# disable my.cnf caching in mysql_root_auth.sh, so that we definitely
# use the root password defined in the cluster
MYSQL_ROOT_AUTH_BYPASS_CHECKS=true source /var/lib/operator-scripts/mysql_root_auth.sh

MARIADB_PIDFILE=/var/lib/mysql/mariadb.pid
function kolla_update_db_root_pw {
    # update the root password given a set of mariadb datafiles

    # because galera controller generates a new root password if one was
    # not sent via pre-existing secret, the root pw has to be updated if
    # existing datafiles are present, as they will still store the previous
    # root pw which we by definition don't know what it is

    # ported from kolla_extend_start
    echo -e "Running with --skip-grant-tables to reset root password"
    rm -fv ${MARIADB_PIDFILE}
    mysqld_safe --skip-grant-tables --wsrep-on=OFF --pid-file=${MARIADB_PIDFILE} &

    # Wait for the mariadb server to be "Ready" before running root update commands
    # NOTE(huikang): the location of mysql's socket file varies depending on the OS distributions.
    # Querying the cluster status has to be executed after the existence of mysql.sock and mariadb.pid.
    TIMEOUT=${DB_MAX_TIMEOUT:-60}
    while [[ ! -S /var/lib/mysql/mysql.sock ]] && \
          [[ ! -S /var/run/mysqld/mysqld.sock ]] || \
          [[ ! -f "${MARIADB_PIDFILE}" ]]; do
        if [[ ${TIMEOUT} -gt 0 ]]; then
            let TIMEOUT-=1
            sleep 1
        else
            echo -e "Surpassed timeout of ${TIMEOUT} without seeing a pidfile"
            exit 1
        fi
    done

    echo -e "Refreshing root passwords"
    mysql -u root <<EOF
FLUSH PRIVILEGES;
ALTER USER 'root'@'localhost' IDENTIFIED BY '$DB_ROOT_PASSWORD';
ALTER USER 'root'@'%'  IDENTIFIED BY '$DB_ROOT_PASSWORD';
FLUSH PRIVILEGES;
EOF
    echo -e "shutting down skip-grant mysql instance"
    mysqladmin -uroot -p"${DB_ROOT_PASSWORD}" shutdown
}


if [ -e /var/lib/mysql/mysql ]; then
    echo -e "Database already exists. Reuse it."
    # set up permissions of mounted directories before starting
    # galera or the sidecar logging container
    sudo -E kolla_set_configs
    kolla_update_db_root_pw
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

PODNAME=$(hostname -f | cut -d. -f1,2)
PODIPV4=$(grep "${PODNAME}" /etc/hosts | grep -v ':' | cut -d$'\t' -f1)
PODIPV6=$(grep "${PODNAME}" /etc/hosts | grep ':' | cut -d$'\t' -f1)

cd /var/lib/config-data/default
for cfg in *.cnf.in; do
    if [ -s "${cfg}" ]; then

        if [[ "" = "${PODIPV6}" ]]; then
            PODIP="${PODIPV4}"
            IPSTACK="IPV4"
        else
            PODIP="[::]"
            IPSTACK="IPV6"
        fi

        echo "Generating config file from template ${cfg}, will use ${IPSTACK} listen address of ${PODIP}"
        sed -e "s/{ PODNAME }/${PODNAME}/" -e "s/{ PODIP }/${PODIP}/" -e "s/{ SSL_CIPHER }/${SSL_CIPHER}/" "/var/lib/config-data/default/${cfg}" > "/var/lib/config-data/generated/${cfg%.in}"
    fi
done
