#!/bin/bash
set -e
if [ -e /var/lib/mysql/mysql ]; then exit 0; fi
#echo -e "\n[mysqld]\nwsrep_provider=none" >> /etc/my.cnf
#kolla_set_configs
#sudo -u mysql -E kolla_extend_start
mkdir -p /var/lib/mysql
mysql_install_db
mysqld_safe --skip-networking --wsrep-on=OFF &
timeout {{.DbMaxTimeout}} /bin/bash -c "until mysqladmin -uroot -p'$DB_ROOT_PASSWORD' ping 2>/dev/null; do sleep 1; done"
mysql -uroot -p"$DB_ROOT_PASSWORD" -e "CREATE USER 'mysql'@'localhost';"
mysql -uroot -p"$DB_ROOT_PASSWORD" -e "REVOKE ALL PRIVILEGES, GRANT OPTION FROM 'mysql'@'localhost';"
timeout {{.DbMaxTimeout}} mysqladmin -uroot -p"$DB_ROOT_PASSWORD" shutdown
