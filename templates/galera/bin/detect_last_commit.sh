#!/bin/bash

set -eu

# Adapted from clusterlab's galera resource agent
recover_args="--datadir=/var/lib/mysql \
                --user=mysql \
                --skip-networking \
                --wsrep-cluster-address=gcomm://localhost"
recovery_file_regex='s/.*WSREP\:.*position\s*recovery.*--log_error='\''\([^'\'']*\)'\''.*/\1/p'
recovered_position_regex='s/.*WSREP\:\s*[R|r]ecovered\s*position.*\:\(.*\)\s*$/\1/p'

# codership/galera#354
# Some ungraceful shutdowns can leave an empty gvwstate.dat on
# disk. This will prevent galera to join the cluster if it is
# configured to attempt PC recovery. Removing that file makes the
# node fall back to the normal, unoptimized joining process.
if [ -f /var/lib/mysql/gvwstate.dat ] && \
   [ ! -s /var/lib/mysql/gvwstate.dat ]; then
    echo "empty /var/lib/mysql/gvwstate.dat detected, removing it to prevent PC recovery failure at next restart" >&2
    rm -f /var/lib/mysql/gvwstate.dat
fi

echo "attempting to detect last commit version by reading grastate.dat" >&2
last_commit="$(cat /var/lib/mysql/grastate.dat | sed -n 's/^seqno.\s*\(.*\)\s*$/\1/p')"
if [ -z "$last_commit" ] || [ "$last_commit" = "-1" ]; then
    tmp=$(mktemp)
    chown mysql:mysql $tmp

    # if we pass here because grastate.dat doesn't exist,
    # try not to bootstrap from this node if possible
    # if [ ! -f /var/lib/mysql/grastate.dat ]; then
    #     set_no_grastate
    # fi

    echo "now attempting to detect last commit version using 'mysqld_safe --wsrep-recover'" >&2

    mysqld_safe --wsrep-recover $recover_args --log-error=$tmp 1>&2

    last_commit="$(cat $tmp | sed -n $recovered_position_regex | tail -1)"
    if [ -z "$last_commit" ]; then
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
                            --tc-heuristic-recover=rollback --log-error=$tmp 2>/dev/null

                last_commit="$(cat $tmp | sed -n $recovered_position_regex | tail -1)"
                if [ ! -z "$last_commit" ]; then
                    echo "State recovered. force SST at next restart for full resynchronization" >&2
                    rm -f /var/lib/mysql/grastate.dat
                    # try not to bootstrap from this node if possible
                    # set_no_grastate
                fi
            fi
        fi
    fi
    rm -f $tmp
fi

if [ ! -z "$last_commit" ]; then
    echo "$last_commit"
    exit 0
else
    echo "Unable to detect last known write sequence number" >&2
    exit 1
fi
