# Pod Remediation for Local PVC Failures


## Overview

In RHOSO, Galera clusters are commonly deployed using local storage for persistent volumes.
While this achieves predictable I/O performance like low latency and high throughput, this
storage model has an important constraint: a local Persistent Volume (PV) is physically tied
to the OCP worker node it resides on.

When an OCP worker node becomes unavailable (e.g. du to hardware failure, network
partition ...), the local PVs hosted on that node become unreachable. The associated
Persistent Volume Claims (PVCs) remain bound to the unavailable node, and Kubernetes will
not reschedule the pod to another node because it cannot mount the local storage there.
The pod stays stuck in `Terminating` or `Pending` state indefinitely.

In regular deployments with the expected anti-affinity rules in place, each OCP worker
hosts a single replica of a Galera cluster. When an OCP worker becomes unavailable, this
means one Galera replica is effectively lost. If the cluster was healthy before the failure,
it continues to operate with one less node. However, the stuck pod blocks the cluster from
ever recovering to full capacity — it cannot rejoin because the storage it depends on is
unreachable.


## The PodRemediator Mechanism

The mariadb-operator integrates with the **PodRemediator** from the
[infra-operator](https://github.com/openstack-k8s-operators/infra-operator) to handle 
recovery for the type of scenario described above. PodRemediator works in conjunction
with OpenShift's Node Health Check (NHC) and Self Node Remediation (SNR) to detect unhealthy
nodes and coordinate the recovery of stuck PVCs.

When PodRemediator can determine that a local PV is 'stuck' after its hosting OCP worker
becomes unavailable, it can coordinate with the mariadb-operator to delete this stuck PV
and its associated PVC and pod, so that OCP can reschedule the pod elsewhere on a working
OCP worker.

The remediation design uses a **consent handshake** between PodRemediator and the mariadb-operator:

- PodRemediator annotates stuck PVCs with `remediation.openstack.org/pvc-stuck-on-node`
  and `remediation.openstack.org/request-id` to signal that a PVC needs to be deleted.
- The mariadb-operator evaluates whether it is safe to delete the PVC and, if so, grants
  consent by setting `remediation.openstack.org/safe-to-delete=true` and echoing back the
  `request-id` in `remediation.openstack.org/consent-id`.
- PodRemediator then deletes the PVC, allowing the pod to be rescheduled to a healthy
  node with a fresh, empty PVC.

This handshake ensures that neither side acts unilaterally. The mariadb-operator never
deletes more than one PVC at a time, preventing data loss in multi-replica clusters.


## Recovery Sequence

The following sequence describes the full recovery path when one OCP worker node becomes
unavailable and one Galera pod is stuck due to a local PVC.

```
NHC/SNR          PodRemediator         mariadb-operator         Galera pod
   |                   |                      |                      |
   |-- detects node -->|                      |                      |
   |   unhealthy       |                      |                      |
   |                   |                      |                      |
   |-- confirms ------>|                      |                      |
   |   fencing         |                      |                      |
   |                   |                      |                      |
   |                   |--- annotates PVC --->|                      |
   |                   |  pvc-stuck-on-node   |                      |
   |                   |  request-id=<uid>    |                      |
   |                   |                      |                      |
   |                   |            EnsurePVCAvailability()          |
   |                   |            detects PVC annotation           |
   |                   |            marks pod in UnavailablePods     |
   |                   |            (Reason=PVCStuck)                |
   |                   |                      |                      |
   |                   |            CheckForPendingPodRemediation()  |
   |                   |            grants consent on PVC:           |
   |                   |            safe-to-delete=true              |
   |                   |            consent-id=<uid>                 |
   |                   |                      |                      |
   |                   |<-- reads consent ----                       |
   |                   |    deletes PVC       |                      |
   |                   |                      |                      |
   |                   |                      |<-- pod rescheduled --+
   |                   |                      |    new PVC created   |
   |                   |                      |    pod starts        |
   |                   |                      |                      |
   |                   |                   findBestCandidate()       |
   |                   |                   excludes unavailable pod  |
   |                   |                   from bootstrap candidates |
   |                   |                      |                      |
   |                   |                      |-- injects ---------->|
   |                   |                      |   gcomm://<peers>    |
   |                   |                      |   (joiner)           |
   |                   |                      |                      |
   |                   |           CheckForResolvedPodRemediation()  |
   |                   |           polls pod: Ready + wsrep Synced?  |
   |                   |                      |                      |
   |                   |                      |<-- wsrep: Primary ---+
   |                   |                      |    Synced            |
   |                   |                      |                      |
   |                   |            clears UnavailablePods entry     |
   |                   |            frees remediation slot           |
```

The sequence diagram details what happens when a single OCP node becomes unavailable:

 - **Node failure detected**: NHC detects the worker node is unhealthy and triggers SNR to
   fence it.

 - **SNR confirms fencing**: PodRemediator detects that a worker node has been fenced.

 - **PVC annotated**: PodRemediator identifies PVCs bound to the fenced node via local PVs
   and annotates them with `pvc-stuck-on-node` (the node name) and `request-id` (a unique
   handshake identifier).

 - **mariadb-operator detects the PVC annotation**: On the next reconcile event,
   `EnsurePVCAvailability()` scans all PVCs belonging to the Galera StatefulSet.
   For each PVC carrying the `pvc-stuck-on-node` annotation, the associated pod is
   added to `Status.UnavailablePods` with `Reason=PVCStuck`.

 - **Consent granted**: `CheckForPendingPodRemediation()` looks for unavailable pods
   that require remediation. If one is found _and_ no other remediation is in progress,
   the pod is marked for remediation and the mariadb-operator patches the stuck PVC with
   `safe-to-delete=true` and `consent-id=<request-id>`. At most one PVC is consented
   at a time per Galera CR; other stuck PVCs wait in `UnavailablePods` until the current
   remediation finishes successfully, i.e. the pod is restarted and synced against the
   running Galera cluster.

 - **PVC deleted**: PodRemediator reads the consent annotations, verifies the request ID
   matches, and deletes the PVC. This unblocks the pod from its stuck state.

 - **Pod rescheduled**: Without the stuck PVC, Kubernetes reschedules the pod to an
   available node. A new, empty PVC is created for the pod's local storage.

 - **Galera pod restart**: when the unblocked Galera pod restarts (with an empty Galera
   database on disk from a fresh PVC), the mariadb-operator can inject a joining gcomm URI
   (`gcomm://<all-peers>`) into the container, so that Galera can restart in the pod and
   rejoin the existing Galera cluster.

 - **Remediation confirmed**: Each reconcile loop calls `CheckForResolvedPodRemediation()`,
   which probes the pod via `mysqld`'s `wsrep_cluster_status` and
   `wsrep_local_state_comment` variables. Once both report `Primary` and `Synced`, the pod
   has fully re-synchronized. The operator clears its entry from `Status.UnavailablePods`
   and frees the remediation slot for any other pending stuck pods.


## Design considerations

### Unblocking a pod causes a PVC to be recreated from scratch

To unblock Kubernetes and allow it to schedule a Galera pod on a working OCP worker,
the PodRemediator has to delete the stuck PVC. The new pod will bind to a newly created
PVC with fresh local storage, which means no database data will be present on disk
at the time this pod is restarted by Kubernetes.

This behavior is by design; as long as the original OCP worker becomes unavailable, it
is not possible to access the original pod's data anyway. Once the new pod is scheduled
on a working OCP worker, it will be able to start Galera and re-sync fully to the running
cluster with a SST (State Snapshot Transfer). So the Galera library takes care of restoring
data automatically.

### Sequential remediation for each Galera CR

The remediation sequence described above relies on an internal state machine that is
designed to allow _only one_ remediation at a time per Galera CR. This is done to
ensure that only one PVC at a time contains old or no data.

Practically, the mariadb-operator serializes PVC deletions. If multiple Galera pods of
the same CR are stuck simultaneously, they are recovered one at a time to avoid deleting
all copies of the data.

It is still possible to have several Galera CR remediated concurrently. For instance,
the default RHOSO deployment consists in two Galera clusters (`openstack` and
`openstack-cell1`), and both cluster can be remediated concurrently, with the limit of
one PVC remediation at a time for each CR.

### Failover scenario and their impact on Quorum

The sequence shown above describes the simple recovery scenario where one pod goes
away, is restarted elsewhere, and it can rejoin the existing Galera cluster. Other
types of recoveries can be performed automatically. They all rely on the fact that
the mariadb-operator takes an opinionated view on Galera Quorum:

 - It disregard unavailable pods for any bootstrap decision
 
 - It assumes unavailable pods always contain an old view of the Galera cluster

Those two assertions allow the recovery to take place in a variety of scenarios.

#### one node is remediated, the existing 2-node cluster is stopped and must be bootstrapped

When a pod is remediated, it contains no data locally, so it has to be restarted as
a joining node. This only works as long as the Galera cluster is still up and running.

If not, the mariadb-operator has to find a candidate to re-bootstrap the cluster. The
logic in function `findBestCandidate()` knows about unavailable pods, and every pod
marked as 'stuck' is deducted from the total number of pods that have to be scanned
before a bootstrap candidate can be determined.

This means that when e.g. one pod is 'stuck', the operator only needs to scan the
remaining two nodes to bootstrap from the most recent one, thus recovering the
Galera cluster and the database service. At this point, the stuck pod can rejoin
the cluster and the remediation will finish once it has fully re-synchronized.

#### two nodes are stuck, only 1-node remains available with data

If two nodes become unavailable at the same time, the last Galera node will lose
quorum, which makes the Galera pod stop, and database service becomes unavailable.

In that scenario, the mariadb-operator takes a "last man standing" approach, and decides
that the remaining Galera database contains the most recent data and should be the
one to bootstrap the Galera cluster until the two other Galera pods can be recovered.

As explained previously, only one remediation will take place at a time, which means
first one pod will be rescheduled on a running OCP worker, it will rejoin the remaining
Galera pod, and synchronize fully against it via an SST. Once this is done, the
remediation slot will be available to recover the second Galera pod.

#### Three nodes become unavailable at the same time

This is the worst case scenario, as no automatic fail-over can take place, since no
data remains available on disk anywhere (all PV are 'stuck').

In this case, the PodRemediator will still try to remediate one pod out of the three
because the remediation slot is available. However, the restarted pod will never be
able to restart galera, because the mariadb-operator correctly flags this pod as
still unavailable (it still has no data and hasn't re-synchronized).

In this case, for an automatic recovery to take place, the original OCP worker will
have to come back into service. At this time, the PodRemediator will lift its
annotations and the mariadb-operator will see the pods as available again. This will
allow a reconcile loop to choose a pod to bootstrap the cluster. It is guaranteed
that the selected node won't be the pod being remediated, as explained previously.
Consequently, the cluster will restart from a valid pod and no data will be lost.


## Configuration

For the mariadb-operator to proceed with pod remediation, a `PodRemediator` CR must
be deployed in the cluster, and both Node Health Check (NHC) and Self Node Remediation
(SNR) operators must be installed and configured in the OCP cluster.

No additional configuration is required in the Galera CR; the mariadb-operator enables
pod remediation automatically when PodRemediator annotations appear on PVCs.

You can instantiate a `PodRemediator` that watches the namespace where Galera is deployed
with the following CR:

```yaml
apiVersion: remediation.openstack.org/v1beta1
kind: PodRemediator
metadata:
  name: podremediator
  namespace: openstack
spec:
  namespaces:
    - openstack
```
