#!/bin/bash

set -ue

function finally {
    if [ -n "${debugpod}" -a -n "$(oc get --ignore-not-found -o name ${debugpod})" ]; then
        oc -n $NAMESPACE delete ${debugpod} --force
    fi
    rm -rf backup-assert
}

trap finally EXIT

jobname=$1

echo -n "Retrieving job pod"
jobpod=$(oc -n $NAMESPACE get pods -l job-name=${jobname} -o name)
debugpod=${jobpod}-debug
test -n "${jobpod}"
echo " - ${jobpod}"

out=backup-assert/${jobpod#pod/}
mkdir -p $out

echo "Extracting job pod's logs"
oc -n $NAMESPACE logs --tail=-1 ${jobpod} > ${out}/logs

backup_tag=$(awk -e '/Starting backup/ {print $NF;exit}' $out/logs)
cluster=$(awk -e '/Starting backup/ {print $(NF-2);exit}' $out/logs)

# TODO Fetching PVC data by running a debug pod seems to cause issues in CI
# echo "Creating a debug pod for extracting backup data"
# oc -n $NAMESPACE debug ${jobpod} -- dumb-init sleep infinity >&/dev/null &
# while oc -n $NAMESPACE get ${debugpod} 2>&1 | grep -q 'not found'; do
#     sleep 1
# done
# oc -n $NAMESPACE wait --for=jsonpath='{.status.phase}'=Running ${debugpod}
# echo "Assert that backup was taken correctly"
# oc -n $NAMESPACE rsh ${debugpod} find /backup/data -name "${cluster}*${backup_tag}*.sql.*" | wc -l | grep -q 2

members=$(sed -ne 's/.*found: \(.*\) (.*/\1/p' ${out}/logs | wc -w)
if [ $members -eq 3 ]; then
    echo "Assert galera backup node != current endpoint"
    endpoint=$(sed -ne 's/.*endpoint: \(.*\))/\1/p' ${out}/logs)
    selected=$(sed -ne 's/.*electing \([^ ]*\) .*/\1/p' ${out}/logs)
    test "$selected" != "$endpoint"
fi

echo "PASSED"
