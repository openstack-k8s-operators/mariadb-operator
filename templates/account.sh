#!/bin/bash

MYSQL_REMOTE_HOST={{.DatabaseHostname}} source /var/lib/operator-scripts/mysql_root_auth.sh

export DatabasePassword=${DatabasePassword:?"Please specify a DatabasePassword variable."}

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.UserName}}'@'localhost' IDENTIFIED BY '$DatabasePassword'{{.RequireTLS}};GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.UserName}}'@'%' IDENTIFIED BY '$DatabasePassword'{{.RequireTLS}};"


# search for the account.  not using SHOW CREATE USER to avoid displaying
# password hash
username=$(mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -NB -e "select user from mysql.user where user='{{.UserName}}' and host='localhost';" )

if [[ ${username} != "{{.UserName}}" ]]; then
    exit 1
fi
