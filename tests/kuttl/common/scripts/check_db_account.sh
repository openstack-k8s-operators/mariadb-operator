#!/bin/sh

set -x

galera="$1"
dbname="$2"
username="$3"
password="$4"


found=0
not_found=1

if [ "$5" = "--reverse" ];then
    # sometimes we want to check that a user does not exist
    found=1
    not_found=0
fi

found_username=$(oc rsh -n ${NAMESPACE} -c galera ${galera} /bin/sh -c 'mysql -uroot -p${DB_ROOT_PASSWORD} -Nse "select user from mysql.user"' | grep -o -w ${username})

# username was not found, exit
if [ -z "$found_username" ]; then
    exit $not_found
fi

# username was found.  if we wanted it to be found, then check the login also.
if [ "$found" = "0" ]; then
    oc rsh -n ${NAMESPACE} -c galera ${galera} /bin/sh -c "mysql -u${username} -p${password} -Nse 'select database();' ${dbname}" || exit -1
fi

exit $found
