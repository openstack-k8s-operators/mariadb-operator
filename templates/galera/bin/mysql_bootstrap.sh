#!/bin/bash
set +eux

# set up $DB_ROOT_PASSWORD.
# disable my.cnf caching in mysql_root_auth.sh, so that we definitely
# use the root password defined in the cluster
MYSQL_ROOT_AUTH_BYPASS_CHECKS=true source /var/lib/operator-scripts/mysql_root_auth.sh


function kolla_update_db_root_pw {
    # update the root password given a set of mariadb datafiles
    # ported from kolla_extend_start with major changes

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

    # Wait for the mariadb server to be "Ready" before running root update commands
    # Querying the cluster status has to be executed after the existence of mysql.sock and mariadb.pid.
    ORIG_TIMEOUT=${DB_MAX_TIMEOUT:-60}
    TIMEOUT=${ORIG_TIMEOUT}
    while [[ ! -S /var/lib/mysql/mysql.sock ]] || \
            [[ ! -f "${CHANGE_PW_PIDFILE}" ]]; do

        if [[ ${TIMEOUT} -gt 0 ]]; then
            let TIMEOUT-=1
            sleep 1
        else
            echo -e "Surpassed timeout of ${ORIG_TIMEOUT} without seeing a pidfile"
            echo -e "Dump of ${CHANGE_PW_LOGFILE}"
            cat ${CHANGE_PW_LOGFILE}
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

    # we definitely started this mysqld so we definitely stop it
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
