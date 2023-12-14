#!/bin/bash

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "DROP USER IF EXISTS '{{.UserName}}'@'localhost'; DROP USER IF EXISTS '{{.UserName}}'@'%';"
