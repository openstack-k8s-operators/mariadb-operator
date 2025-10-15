#!/bin/bash

MYSQL_REMOTE_HOST={{.DatabaseHostname}} source /var/lib/operator-scripts/mysql_root_auth.sh

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "DROP DATABASE IF EXISTS {{.DatabaseName}};"

if [[ "${DatabasePassword}" != "" ]]; then
    # legacy; drop the username also
    # we can't do this unconditionally here because we only want to delete the
    # mysql account if the MariaDBDatabase was using the legacy "secret" attribute;
    # otherwise this could be from a valid MariaDBAccount
    mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "DROP USER IF EXISTS '{{.DatabaseName}}'@'localhost'; DROP USER IF EXISTS '{{.DatabaseName}}'@'%';"
fi
