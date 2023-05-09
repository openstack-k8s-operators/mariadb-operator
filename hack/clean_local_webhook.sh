#!/bin/bash
set -ex

oc delete validatingwebhookconfiguration/vmariadb.kb.io --ignore-not-found
oc delete mutatingwebhookconfiguration/mmariadb.kb.io --ignore-not-found
