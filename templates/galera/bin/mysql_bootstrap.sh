#!/bin/bash
set +eux
PODNAME=$(hostname -f | cut -d. -f1,2)
PODIP=$(grep "${PODNAME}" /etc/hosts | cut -d$'\t' -f1)
pushd /var/lib/config-data
for cfg in *.cnf.in; do
    if [ -s "${cfg}" ]; then
        echo "Generating config file from template ${cfg}"
        sed -e "s/{ PODNAME }/${PODNAME}/" -e "s/{ PODIP }/${PODIP}/" "/var/lib/config-data/${cfg}" > "/var/lib/pod-config-data/${cfg%.in}"
    fi
done
popd
if [ -e /var/lib/mysql/mysql ]; then
    echo -e "Database already bootstrapped"
    exit 0
fi
echo -e "\n[mysqld]\nwsrep_provider=none" >> /etc/my.cnf
kolla_set_configs
sudo -u mysql -E kolla_extend_start
