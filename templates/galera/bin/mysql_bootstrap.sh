#!/bin/bash
set +eux


function wait_for_mysqld_readiness {
    # Wait for the mariadb server to be "Ready" before running commands
    # Querying the cluster status has to be executed after the existence of mysql.sock and mariadb.pid.
    local mysql_pid_file=$1
    local mysql_log_file=$2

    ORIG_TIMEOUT=${DB_MAX_TIMEOUT:-60}
    TIMEOUT=${ORIG_TIMEOUT}
    while [[ ! -S /var/lib/mysql/mysql.sock ]] || \
            [[ ! -f "${mysql_pid_file}" ]]; do

        if [[ ${TIMEOUT} -gt 0 ]]; then
            let TIMEOUT-=1
            sleep 1
        else
            echo -e "Surpassed timeout of ${ORIG_TIMEOUT} without seeing a pidfile"
            echo -e "Dump of ${mysql_log_file}"
            cat "${mysql_log_file}"
            exit 1
        fi
    done
}

function update_db_root_pw {
    # update the root password given a set of mariadb datafiles

    # because galera controller generates a new root password if one was
    # not sent via pre-existing secret, the root pw has to be updated if
    # existing datafiles are present, as they will still store the previous
    # root pw which we by definition don't know what it is

    # to achieve this, we have to run our own mysqld.

    # before we do any of that, check if the socket file exists and we can log
    # in with the current password. this happens if we are running in a debug
    # container for example; sock file is there, works, lets us log in to
    # the mysqld running on the primary container.  do nothing in that case
    #
    # TOPIC: Why does /var/lib/mysql/mysql.sock allow the debug container to
    # communicate with the primary container?
    # Having two containers communicate over a sockfile only works when both
    # containers are on the same host.  "oc debug" will run from the same
    # host because:
    #
    # 1. https://bugzilla.redhat.com/show_bug.cgi?id=1806662 - "oc debug of pod
    #    with host mounts should target the same node"
    # 2. ReadWriteOnce volumes enforce single-node mounting, and we mount with
    #    RWO
    #    (https://cookbook.openshift.org/storing-data-in-persistent-volumes/how-can-i-scale-applications-using-rwo-persistent-volumes.html)
    #
    # So it is functionally impossible for a container from another host to
    # mount this volume at the same time, we can be assured we're on the same
    # host in oc debug if we see these files.
    #
    if [[ -S /var/lib/mysql/mysql.sock ]]; then
        echo -e "Socket file /var/lib/mysql/mysql.sock exists, testing current root password"
        if timeout 5 mysql -u root -p"${DB_ROOT_PASSWORD}" -e "SELECT 1;" &>/dev/null; then
            echo -e "Successfully logged in via existing /var/lib/mysql/mysql.sock file with current root password, not attempting to change password"

            # hilarity avoided.   trying to run another mysqld when another container
            # is running one against the same /var/lib/mysql fails to start, claiming
            # exclusive lock issues.  these exclusive lock issues can only be
            # detected by reading the logfile of the failed mysqld process
            # (flock, fcntl, etc. cannot detect it).  this is a slow process
            # that's best avoided if not needed anyway.
            return
        else
            echo -e "Could not log in with current password, proceeding with password reset"
        fi
    fi

    # OK there's no sockfile or we couldn't log in.  let's do this

    # first, use log/pidfile on a non-mounted volume, so that we can see what
    # happens local to this container (which might be a debug container)
    # without leaking details from other processes that may share /var/lib/mysql
    CHANGE_PW_PIDFILE=/var/tmp/updatepw.pid
    CHANGE_PW_LOGFILE=/var/tmp/updatepw.log

    echo -e "Running with --skip-grant-tables to reset root password"
    rm -fv ${CHANGE_PW_PIDFILE}  ${CHANGE_PW_LOGFILE}
    mysqld_safe --skip-grant-tables --wsrep-on=OFF --log-error=${CHANGE_PW_LOGFILE} --pid-file=${CHANGE_PW_PIDFILE} &
    wait_for_mysqld_readiness "$CHANGE_PW_PIDFILE" "$CHANGE_PW_LOGFILE"

    echo -e "Refreshing root passwords"
    mysql -u root <<EOF
FLUSH PRIVILEGES;
ALTER USER 'root'@'localhost' IDENTIFIED BY '$DB_ROOT_PASSWORD';
-- create root@% if missing (e.g. dropped during a failed password rotation)
CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED BY '$DB_ROOT_PASSWORD';
ALTER USER 'root'@'%' IDENTIFIED BY '$DB_ROOT_PASSWORD';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;
FLUSH PRIVILEGES;
EOF

    # we definitely started this mysqld so we definitely stop it
    mysqladmin -uroot -p"${DB_ROOT_PASSWORD}" shutdown
}

function setup_new_database {
    echo -e "Creating the database on disk"
    mysql_install_db --datadir=/var/lib/mysql/e --user=mysql --skip-test-db
    mv /var/lib/mysql/e/* /var/lib/mysql
    rm -rf /var/lib/mysql/e

    echo -e "Running mysqld with --skip-grant-tables to set up new database"
    SETUP_PIDFILE=/var/tmp/setup.pid
    SETUP_LOGFILE=/var/tmp/setup.log
    rm -fv ${SETUP_PIDFILE}  ${SETUP_LOGFILE}
    mysqld_safe --skip-grant-tables --wsrep-on=OFF --log-error=${SETUP_LOGFILE} --pid-file=${SETUP_PIDFILE} &
    wait_for_mysqld_readiness "$SETUP_PIDFILE" "$SETUP_LOGFILE"

    # This mimicks what mysql_secure_installation does, but it keeps
    # 'root'@'%' because that is what is used to create/delete users
    # linked to MariaDBAccount and MariaDBDatabases
    echo -e "Refreshing root passwords"
    mysql -u root <<EOF
FLUSH PRIVILEGES;
ALTER USER 'root'@'localhost' IDENTIFIED BY '$DB_ROOT_PASSWORD';
CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED BY '$DB_ROOT_PASSWORD';
ALTER USER 'root'@'%' IDENTIFIED BY '$DB_ROOT_PASSWORD';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;
DELETE FROM mysql.user WHERE User='';
FLUSH PRIVILEGES;
EOF

    # we definitely started this mysqld so we definitely stop it
    # use credentials from the generated my.cnf credential file
    mysqladmin -uroot shutdown
}

function setup_configs_and_credentials {
    # Prepare space for invocations of mysql_root_auth.sh to place pw cache
    # file. We do this by running the mysql_root_auth.sh which will also set
    # up $DB_ROOT_PASSWORD for the remainder of this script.
    #
    # OSPRH-27031: The conditional is for backwards compatibility with old pods
    # where script is updated but mysql_root_auth.sh is not yet available; in those
    # cases we rely upon DB_ROOT_PASSWORD already part of the pods environment as
    # was the case before the introduction of mysql_root_auth.sh.
    #
    if [ -f /var/lib/operator-scripts/mysql_root_auth.sh ]; then
        # MYSQL_ROOT_AUTH_BYPASS_CHECKS=true: disable my.cnf caching in
        # mysql_root_auth.sh, so that we definitely use the root password defined in
        # the cluster, not whatever possibly stale PW is in local files.
        MYSQL_ROOT_AUTH_BYPASS_CHECKS=true source /var/lib/operator-scripts/mysql_root_auth.sh
        if [ $? -ne 0 ]; then
            echo -e "Could not retrieve root password from K8s API, aborting bootstrap"
            exit 1
        fi
    else
        echo -e "Could not access /var/lib/operator-scripts/mysql_root_auth.sh script; "
        echo -e "using environment DB_ROOT_PASSWORD and creating pw cache directory manually"
        export MYSQL_PWD="${DB_ROOT_PASSWORD}"

        PW_CACHE_DIR=/var/local/my.cnf
        mkdir -p ${PW_CACHE_DIR}
        echo -e "Created ${PW_CACHE_DIR} pw cache directory"
    fi

    if [ -z "${DB_ROOT_PASSWORD}" ]; then
        echo -e "DB_ROOT_PASSWORD is blank, aborting bootstrap"
        exit 1
    fi

}

if [ -e /var/lib/mysql/mysql ]; then
    echo -e "Database already exists. Reuse it."

    # make sure the generated directory starts clean.
    rm -f /var/lib/config-data/generated/*.cnf
    touch /var/lib/config-data/generated/galera.cnf

    setup_configs_and_credentials

    update_db_root_pw
else
    echo -e "Creating new mariadb database."
    cat <<EOF >/var/lib/config-data/generated/galera.cnf
[client]
!includedir /var/local/my.cnf/

[mysqld]
bind_address=localhost
wsrep_provider=none
EOF

    setup_configs_and_credentials
    setup_new_database
fi

# Generate the mariadb configs from the templates; they are written
# to the config-data-generated EmptyDir which is also mounted at
# /etc/my.cnf.d/ so the main container picks them up directly.
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
