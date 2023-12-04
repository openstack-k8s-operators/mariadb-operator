#!/bin/bash
export DatabasePassword=${DatabasePassword:?"Please specify a DatabasePassword variable."}

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.UserName}}'@'localhost' IDENTIFIED BY '$DatabasePassword';GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.UserName}}'@'%' IDENTIFIED BY '$DatabasePassword';"
