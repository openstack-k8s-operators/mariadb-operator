#!/bin/bash

MYSQL_REMOTE_HOST="{{.DatabaseHostname}}" source /var/lib/operator-scripts/mysql_root_auth.sh

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "CREATE DATABASE IF NOT EXISTS {{.DatabaseName}}; ALTER DATABASE {{.DatabaseName}} CHARACTER SET '{{.DefaultCharacterSet}}' COLLATE '{{.DefaultCollation}}';"

if [[ "${DatabasePassword}" != "" ]]; then
    # legacy; create database with username
    mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'localhost' IDENTIFIED BY '$DatabasePassword'{{.DatabaseUserTLS}};GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'%' IDENTIFIED BY '$DatabasePassword'{{.DatabaseUserTLS}};"
fi

# echo the SHOW CREATE to ensure db was created; will return nonzero error code if
# DB does not exist
mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -NB -e "SHOW CREATE DATABASE {{.DatabaseName}};"
