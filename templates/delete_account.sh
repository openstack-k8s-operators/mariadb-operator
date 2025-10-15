#!/bin/bash

MYSQL_REMOTE_HOST={{.DatabaseHostname}} source /var/lib/operator-scripts/mysql_root_auth.sh

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "DROP USER IF EXISTS '{{.UserName}}'@'localhost'; DROP USER IF EXISTS '{{.UserName}}'@'%';"
