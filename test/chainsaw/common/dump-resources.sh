#!/bin/bash

# Dump various resources still running in the environment, for debugging purpose
# This is useful because all resources created by chainsaw are automatically deleted
# before the we capture logs or the environment state in CI
oc -n $NAMESPACE get jobs
oc -n $NAMESPACE get pods
oc -n $NAMESPACE get pvc
oc -n $NAMESPACE get pv

# Optional objects to be described
for i in $*; do
    echo inspecting $i
    oc -n $NAMESPACE describe $i
    oc -n $NAMESPACE logs $i
done
true
