#!/bin/bash

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "DROP DATABASE IF EXISTS {{.DatabaseName}}; DROP USER IF EXISTS '{{.DatabaseName}}'@'localhost'; DROP USER IF EXISTS '{{.DatabaseName}}'@'%';"
