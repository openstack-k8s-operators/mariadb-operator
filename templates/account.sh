#!/bin/bash

MYSQL_REMOTE_HOST="{{.DatabaseHostname}}" source /var/lib/operator-scripts/mysql_root_auth.sh

export DatabasePassword=${DatabasePassword:?"Please specify a DatabasePassword variable."}

MYSQL_CMD="mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306"

if [ -n "{{.DatabaseName}}" ]; then
    GRANT_DATABASE="{{.DatabaseName}}"
else
    GRANT_DATABASE="*"
fi

# going for maximum compatibility here:
# 1. MySQL 8 no longer allows implicit create user when GRANT is used
# 2. MariaDB has "CREATE OR REPLACE", but MySQL does not
# 3. create user with CREATE but then do all password and TLS with ALTER to
#    support updates

$MYSQL_CMD <<EOF
CREATE USER IF NOT EXISTS '{{.UserName}}'@'localhost';
CREATE USER IF NOT EXISTS '{{.UserName}}'@'%';

ALTER USER '{{.UserName}}'@'localhost' IDENTIFIED BY '$DatabasePassword'{{.RequireTLS}};
ALTER USER '{{.UserName}}'@'%'  IDENTIFIED BY '$DatabasePassword'{{.RequireTLS}};

GRANT ALL PRIVILEGES ON ${GRANT_DATABASE}.* TO '{{.UserName}}'@'localhost';
GRANT ALL PRIVILEGES ON ${GRANT_DATABASE}.* TO '{{.UserName}}'@'%';
EOF

# If we just changed the root password, update MYSQL_CMD to use the new password
if [ "{{.UserName}}" = "{{.DatabaseAdminUsername}}" ]; then
    MYSQL_CMD="mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -p${DatabasePassword} -P 3306"
fi

# search for the account.  not using SHOW CREATE USER to avoid displaying
# password hash
username=$($MYSQL_CMD -NB -e "select user from mysql.user where user='{{.UserName}}' and host='localhost';" )

if [[ ${username} != "{{.UserName}}" ]]; then
    exit 1
fi
