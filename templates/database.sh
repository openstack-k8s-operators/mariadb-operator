#!/bin/bash
export DatabasePassword=${DatabasePassword:?"Please specify a DatabasePassword variable."}

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "CREATE DATABASE IF NOT EXISTS {{.DatabaseName}}; ALTER DATABASE {{.DatabaseName}} CHARACTER SET '{{.DefaultCharacterSet}}' COLLATE '{{.DefaultCollation}}'; GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'localhost' IDENTIFIED BY '$DatabasePassword';GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'%' IDENTIFIED BY '$DatabasePassword';"
