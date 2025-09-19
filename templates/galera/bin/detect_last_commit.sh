#!/bin/bash

set -eu

source /var/lib/operator-scripts/root_auth.sh


# Adapted from clusterlab's galera resource agent
recover_args="--datadir=/var/lib/mysql \
                --user=mysql \
                --skip-networking \
                --wsrep-cluster-address=gcomm://localhost"
recovery_file_regex='s/.*WSREP\:.*position\s*recovery.*--log_error='\''\([^'\'']*\)'\''.*/\1/p'
recovered_position_uuid_regex='s/.*WSREP\:\s*[R|r]ecovered\s*position\:\ \(.*\)\:.*$/\1/p'
recovered_position_seqno_regex='s/.*WSREP\:\s*[R|r]ecovered\s*position.*\:\(.*\)\s*$/\1/p'

grastate_file=/var/lib/mysql/grastate.dat
gvwstate_file=/var/lib/mysql/gvwstate.dat

uuid=""
seqno=""
safe_to_bootstrap=0
no_grastate=0

function json_summary {
    declare -a out
    if [ -n "$uuid" ]; then out+=( "\"uuid\":\"$uuid\"" ); fi
    if [ -n "$seqno" ]; then out+=( "\"seqno\":\"$seqno\"" ); fi
    if [ $safe_to_bootstrap -ne 0 ]; then out+=( '"safe_to_bootstrap":true' ); fi
    if [ $no_grastate -ne 0 ]; then out+=( '"no_grastate":true' ); fi
    IFS=, ; echo "{${out[*]}}"
}

trap json_summary EXIT

# codership/galera#354
# Some ungraceful shutdowns can leave an empty gvwstate.dat on
# disk. This will prevent galera to join the cluster if it is
# configured to attempt PC recovery. Removing that file makes the
# node fall back to the normal, unoptimized joining process.
if [ -f $gvwstate_file ] && \
   [ ! -s $gvwstate_file ]; then
    echo "empty $gvwstate_file detected, removing it to prevent PC recovery failure at next restart" >&2
    rm -f $gvwstate_file
fi

# Attempt to retrieve the seqno information and safe_to_bootstrap hint
# from the saved state file on disk

if [ -f $grastate_file ]; then
    uuid="$(cat $grastate_file | sed -n 's/^uuid.\s*\(.*\)\s*$/\1/p')"
    seqno="$(cat $grastate_file | sed -n 's/^seqno.\s*\(.*\)\s*$/\1/p')"
    safe_to_bootstrap="$(cat $grastate_file | sed -n 's/^safe_to_bootstrap.\s*\(.*\)\s*$/\1/p')"

    if [ -z "$uuid" ] || \
       [ "$uuid" = "00000000-0000-0000-0000-000000000000" ]; then
        safe_to_bootstrap=0
    fi
    if [ "$safe_to_bootstrap" = "1" ]; then
        if [ -z "$seqno" ] || [ "$seqno" = "-1" ]; then
            safe_to_bootstrap=0
        fi
    fi
fi

# If the seqno could not be retrieved, inspect the mysql database

if [ -z "$seqno" ] || [ "$seqno" = "-1" ]; then
    tmp=$(mktemp)
    chown mysql:mysql $tmp

    # if we pass here because grastate.dat doesn't exist, report it
    if [ ! -f /var/lib/mysql/grastate.dat ]; then
        no_grastate=1
    fi

    mysqld_safe --wsrep-recover $recover_args --log-error=$tmp >/dev/null

    seqno="$(cat $tmp | sed -n "$recovered_position_seqno_regex" | tail -1)"
    uuid="$(cat $tmp | sed -n "$recovered_position_uuid_regex" | tail -1)"
    if [ -z "$seqno" ]; then
        # Galera uses InnoDB's 2pc transactions internally. If
        # server was stopped in the middle of a replication, the
        # recovery may find a "prepared" XA transaction in the
        # redo log, and mysql won't recover automatically

        recovery_file="$(cat $tmp | sed -n $recovery_file_regex)"
        if [ -e $recovery_file ]; then
            cat $recovery_file | grep -q -E '\[ERROR\]\s+Found\s+[0-9]+\s+prepared\s+transactions!' 2>/dev/null
            if [ $? -eq 0 ]; then
                # we can only rollback the transaction, but that's OK
                # since the DB will get resynchronized anyway
                echo "local node was not shutdown properly. Rollback stuck transaction with --tc-heuristic-recover" >&2
                mysqld_safe --wsrep-recover $recover_args \
                            --tc-heuristic-recover=rollback --log-error=$tmp >/dev/null 2>&1

                seqno="$(cat $tmp | sed -n "$recovered_position_seqno_regex" | tail -1)"
                uuid="$(cat $tmp | sed -n "$recovered_position_uuid_regex" | tail -1)"
                if [ ! -z "$seqno" ]; then
                    echo "State recovered. force SST at next restart for full resynchronization" >&2
                    rm -f /var/lib/mysql/grastate.dat
                    # try not to bootstrap from this node if possible
                    no_grastate=1
                fi
            fi
        fi
    fi
    rm -f $tmp
fi


if [ -z "$seqno" ]; then
    echo "Unable to detect last known write sequence number" >&2
    exit 1
fi

# json data is printed on exit
