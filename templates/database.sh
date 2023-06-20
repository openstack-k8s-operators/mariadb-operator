#!/bin/bash

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "CREATE DATABASE IF NOT EXISTS {{.DatabaseName}}; ALTER DATABASE {{.DatabaseName}} CHARACTER SET '{{.DefaultCharacterSet}}' COLLATE '{{.DefaultCollation}}';"

if [[ "${DatabasePassword}" != "" ]]; then
    # legacy; create database with username
    mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'localhost' IDENTIFIED BY '$DatabasePassword'{{.DatabaseUserTLS}};GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'%' IDENTIFIED BY '$DatabasePassword'{{.DatabaseUserTLS}};"
fi
