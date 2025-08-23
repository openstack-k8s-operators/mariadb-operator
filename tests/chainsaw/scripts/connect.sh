#!/bin/bash

PID=
connect() {
    exec 6>&- 7<&-
    stdbuf -oL mysql --verbose -uroot -p$DB_ROOT_PASSWORD -h openstack -sN --raw --batch </tmp/cmd >/tmp/out &
    exec 6> /tmp/cmd 7< /tmp/out
    PID=$!
}
last=""
mkfifo /tmp/cmd /tmp/out
while true; do
    kill -0 $PID 2>/dev/null || connect
    echo "show variables like 'wsrep_node_name';" >&6
    # read 4 status line before the mysql answer
    for i in `seq 5`; do
        read -r out <&7;
    done
    out=$(echo "$out" | sed -ne 's/^wsrep_.*\t//p')
    if [ -n "$out" -a "$out" != "$last" ]; then
        last=$out
        echo $last
    fi
    sleep 0.5
done
