/*
Copyright 2022-2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bufio"
	"bytes"
	"context"

	remediationv1 "github.com/openstack-k8s-operators/infra-operator/apis/remediation/v1beta1"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/util/podutils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// isGaleraPrimaryAndSynced checks whether mysqld is running in the Primary
// component and its local state is 'Synced'.
func isGaleraPrimaryAndSynced(ctx context.Context, pod *corev1.Pod, instance *mariadbv1.Galera, h *helper.Helper, config *rest.Config) bool {
	var status []string
	err := mariadb.ExecInPod(ctx, h, config, instance.Namespace, pod.Name, "galera",
		[]string{"mysql", "-nNe", `select variable_value from
information_schema.global_status where variable_name in
('wsrep_cluster_status', 'wsrep_local_state_comment') order by variable_name;`},
		func(stdout *bytes.Buffer, _ *bytes.Buffer) error {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				status = append(status, scanner.Text())
			}
			return nil
		})
	return err == nil && len(status) == 2 && status[0] == "Primary" && status[1] == "Synced"
}

// Iterate through all existing PVCs of a given Galera CR to ensure none of
// them have been marked as "unreachable" by the PodRemediator, as an unreachable
// PVC claiming local storage would prevent its associated pod from being restarted
// on another k8s worker node. If one PVC is found "unreachable", mark its associated
// pod as "unavailable" for the operator, and initiate PVC recovery if no other
// recovery is in progress.
//
// Note that this approach serializes recoveries; the exact recovery steps and
// state assumptions is explain in CheckForStuckPVCRequiringRemediation().
func (r *GaleraReconciler) EnsurePVCAvailability(
	ctx context.Context,
	instance *mariadbv1.Galera,
	helper *helper.Helper,
) error {
	if instance.Spec.Replicas == nil {
		return nil
	}

	log := helper.GetLogger()

	// If we determine that an ongoing remediation has finished, we can clean up
	// our tracking status and free up the remediation slot for further remediations.
	if err := r.CheckForResolvedPodRemediation(ctx, instance, helper); err != nil {
		return err
	}

	// From here, a pod remediation may still be ongoing for this galera CR
	remediationPodName, remediationInProgress := instance.Status.RemediationInProgress()

	// Either way, iterate over all PVCs to look for the "unreachable" ones
	stsName := mariadb.StatefulSetName(instance.Name)
	for i := int32(0); i < *instance.Spec.Replicas; i++ {
		// Check whether a PVC exists for this ordinal pod replica
		pvcName := mariadb.PVCName(stsName, i)
		pvc := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: instance.Namespace}, pvc); err != nil {
			if k8s_errors.IsNotFound(err) {
				continue
			}
			return err
		}

		// Last known status of the associated pod, as recorded in the Galera CR
		podName := mariadb.PodNameFromPVCName(pvcName)
		unavailableStatus, podUnavailable := instance.Status.UnavailablePods[podName]

		// If the PVC has been flagged as "stuck" by the
		// PodRemediator, recall its associated pod is now "unavailable"
		node, found := pvc.Annotations[remediationv1.PVCStuckOnNodeAnnotation]
		if found {
			if !podUnavailable || unavailableStatus.Reason != mariadbv1.PodUnavailabilityReasonPVCStuck {
				log.Info("WARN: Pod blocked due to local PVC being unreachable",
					"Pod", podName, "pvc", pvcName, "node", node)
			}

			desc := mariadbv1.UnavailablePodStatus{
				Reason:      mariadbv1.PodUnavailabilityReasonPVCStuck,
				Node:        node,
				Remediation: false,
			}

			if !remediationInProgress {
				// If no remediation is ongoing, recall to start one for this pod
				desc.Remediation = true
				remediationInProgress = true
			} else if remediationPodName == podName {
				// If this pod was being remediated, do not lose track of it
				desc.Remediation = true
			}

			if desc.Remediation {
				log.Info("Marking pod for remediation", "pod", podName)
			}

			if instance.Status.UnavailablePods == nil {
				instance.Status.UnavailablePods = make(map[string]mariadbv1.UnavailablePodStatus)
			}
			instance.Status.UnavailablePods[podName] = desc
		} else {
			// otherwise, the PVC is not "stuck", make sure there is no
			// lingering pod status in the galera CR status (e.g. if the
			// CR status and the real env's status are no longer in sync)
			if !remediationInProgress && podUnavailable {
				log.Info("Pod no longer unavailable, status cleared", "pod", podName)
				delete(instance.Status.UnavailablePods, podName)
				if len(instance.Status.UnavailablePods) == 0 {
					instance.Status.UnavailablePods = nil
				}
			}
		}
	}

	// At this point, all pods currently unavailable have been tagged,
	// So check whether a new remediation action must be initiated or
	// if an existing remediation must be tracked.
	if err := r.CheckForPendingPodRemediation(ctx, instance, helper); err != nil {
		return err
	}

	return nil
}

// CheckForPendingPodRemediation iterates instance.Status.UnavailablePods
// and looks for remediation actions to be performed.
//   - If a status has Remediation == true, tell the PodRemediator that is it
//     safe to delete its associated PVC.
//   - By design, at most one pod has Remediation == true, so one cannot
//     delete all the PVC for a Galera CR and lose its data.
//   - Even after the PodRemediator has deleted a PVC, the Remediation flag
//     is only cleared once the associated pod has restarted galera, its
//     readiness probe passed, and the local galera reports itself as "Synced"
//   - Each reconcile loop checks whether the Remediation flag can be lifted,
//     freeing up space for other pending remediations if there are some.
func (r *GaleraReconciler) CheckForPendingPodRemediation(
	ctx context.Context,
	instance *mariadbv1.Galera,
	helper *helper.Helper,
) error {
	log := helper.GetLogger()

	if instance.Spec.Replicas == nil || *instance.Spec.Replicas == 0 {
		return nil
	}

	podName, inProgress := instance.Status.RemediationInProgress()
	if !inProgress {
		return nil
	}

	// If the PVC doesn't exist, the PodRemediator unblocked the pod,
	// so the pod should restart and resync
	pvcName := mariadb.PVCNameFromPod(podName)
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: instance.Namespace}, pvc); err != nil {
		if k8s_errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// If that PVC associated to this pod does no longer have annotations
	// from the PodRemediator, this means it has been deleted and
	// recreated elsewhere already. The pod's status will be clean up by
	// CheckForResolvedPodRemediation() in subsequent reconcile events.
	_, stuck := pvc.Annotations[remediationv1.PVCStuckOnNodeAnnotation]
	if !stuck {
		return nil
	}

	// If the mariadb-operator annotated the PVC for the PodRemediator,
	// nothing left to be done until the pod restarts and its local
	// galera is synced
	if pvc.Annotations[remediationv1.SafeToDeleteAnnotation] == "true" {
		return nil
	}

	// Consent requires echoing the request ID back as consent ID so that
	// PodRemediator's HasRemediationConsent check passes. Never grant consent
	// for an empty request ID — it would match any subsequent handshake.
	requestID := pvc.Annotations[remediationv1.RequestIDAnnotation]
	if requestID == "" {
		log.Info("PVC has no request-id; deferring consent until PodRemediator sets one",
			"pod", podName, "pvc", pvcName)
		return nil
	}

	// Authorize deletion of the unreachable PVC by setting both safe-to-delete
	// and consent-id in a single patch. No other PVC will be processed until
	// the associated pod restarts and its local galera is synced.
	oldPVC := pvc.DeepCopy()
	if pvc.Annotations == nil {
		pvc.Annotations = map[string]string{}
	}
	pvc.Annotations[remediationv1.SafeToDeleteAnnotation] = "true"
	pvc.Annotations[remediationv1.ConsentIDAnnotation] = requestID
	// The resource-version check keeps this consent tied to the request ID read
	// above; if PodRemediator changed the PVC meanwhile, the patch must be retried.
	patch := client.MergeFromWithOptions(oldPVC, client.MergeFromWithOptimisticLock{})
	if err := r.Patch(ctx, pvc, patch); err != nil {
		log.Error(err, "Failed to set safe-to-delete on PVC", "pod", podName, "pvc", pvcName)
		return err
	}
	log.Info("Processing recovery via PodRemediator", "pod", podName, "pvc", pvcName)

	return nil
}

// CheckForResolvedPodRemediation iterates instance.Status.UnavailablePods
// and looks for remediation that can be determined "finished".
//   - If a status has Remediation == true, check whether the associated
//     pod is ready and galera status is "Primary" and "Synced", and if sdo clear the
//     remediation status, freeing up space for the next remediation.
//   - By design, at most one pod has Remediation == true, so only one
//     pod is checked per reconcile loop.
func (r *GaleraReconciler) CheckForResolvedPodRemediation(
	ctx context.Context,
	instance *mariadbv1.Galera,
	helper *helper.Helper,
) error {
	log := helper.GetLogger()

	podName, inProgress := instance.Status.RemediationInProgress()
	if !inProgress {
		return nil
	}

	// If the pod doesn't exist, remediation isn't finished
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: instance.Namespace}, pod); err != nil {
		if k8s_errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// If the pod isn't ready, remediation isn't finished
	if !podutils.IsPodReady(pod) {
		return nil
	}

	// If one can't retrieve the expected status from the galera node when
	// probing mysqld in the pod's galera container, remediation isn't finished
	if !isGaleraPrimaryAndSynced(ctx, pod, instance, helper, r.config) {
		return nil
	}

	// Otherwise, the previously unavailable pod has resynchronized against then
	// galera cluster, so we can clear its remediation state to free the
	// remediation slot for the another pending pod, if any.
	log.Info("Remediation complete, pod unavailability status cleared", "pod", podName)
	delete(instance.Status.UnavailablePods, podName)
	if len(instance.Status.UnavailablePods) == 0 {
		instance.Status.UnavailablePods = nil
	}

	return nil
}
