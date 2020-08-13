#!/bin/bash
export DatabasePassword=${DatabasePassword:?"Please specify a DatabasePassword variable."}

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "CREATE DATABASE IF NOT EXISTS {{.SchemaName}}; GRANT ALL PRIVILEGES ON {{.SchemaName}}.* TO '{{.SchemaName}}'@'localhost' IDENTIFIED BY '$DatabasePassword';GRANT ALL PRIVILEGES ON {{.SchemaName}}.* TO '{{.SchemaName}}'@'%' IDENTIFIED BY '$DatabasePassword';"
