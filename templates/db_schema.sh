#!/bin/bash
# Secrets are obtained from the /var/lib/secrets/ volume.
for X in DatabasePassword; do
  if [[ ! -f "/var/lib/schema-secrets/$X" ]] ; then
     echo "Missing secret for $X. Please specify this secret and try again."
     exit 1
  fi
done

export DatabasePassword="$(cat /var/lib/schema-secrets/DatabasePassword)"

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "CREATE DATABASE IF NOT EXISTS {{.SchemaName}}; GRANT ALL PRIVILEGES ON {{.SchemaName}}.* TO '{{.SchemaName}}'@'localhost' IDENTIFIED BY '$DatabasePassword';GRANT ALL PRIVILEGES ON {{.SchemaName}}.* TO '{{.SchemaName}}'@'%' IDENTIFIED BY '$DatabasePassword';"
