/*
Copyright 2022.

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
	"bytes"
	"context"
	"strings"

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

// isGaleraRunningAndSynced checks whether mysqld is running and wsrep status is 'Synced'
func isGaleraRunningAndSynced(ctx context.Context, pod *corev1.Pod, instance *mariadbv1.Galera, h *helper.Helper, config *rest.Config) bool {
	synced := false
	err := mariadb.ExecInPod(ctx, h, config, instance.Namespace, pod.Name, "galera",
		[]string{"mysql", "-nNe", "select variable_value from information_schema.global_status where variable_name = 'wsrep_local_state_comment';"},
		func(stdout *bytes.Buffer, _ *bytes.Buffer) error {
			predicate := strings.TrimSuffix(stdout.String(), "\n")
			synced = (predicate == "Synced")
			return nil
		})
	return err == nil && synced
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
	availableReplicas int32,
) error {
	if instance.Spec.Replicas == nil {
		return nil
	}

	// Keep the remediation slot occupied until the replacement Galera member
	// is ready and synced, then allow another PVC to be considered.
	if err := r.CheckForResolvedPodRemediation(ctx, instance, helper); err != nil {
		return err
	}

	if err := r.CheckForPendingPodRemediation(ctx, instance, helper, availableReplicas); err != nil {
		return err
	}

	log := helper.GetLogger()

	stsName := mariadb.StatefulSetName(instance.Name)

	for i := int32(0); i < *instance.Spec.Replicas; i++ {
		pvcName := mariadb.PVCName(stsName, i)
		pvc := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: instance.Namespace}, pvc); err != nil {
			if k8s_errors.IsNotFound(err) {
				continue
			}
			return err
		}
		// If the PVC has been flagged as "unreachable" by the
		// PodRemediator, recall its associated pod is "unavailable"
		node, found := pvc.Annotations[remediationv1.PVCStuckOnNodeAnnotation]
		if found {
			if instance.Status.UnavailablePods == nil {
				instance.Status.UnavailablePods = make(map[string]mariadbv1.UnavailablePodStatus)
			}
			pod := mariadb.PodNameFromPVC(pvcName)
			desc := mariadbv1.UnavailablePodStatus{
				Reason: mariadbv1.PodUnavailabilityReasonPVCStuck,
				Node:   node,
			}

			if _, unavail := instance.Status.UnavailablePods[pod]; unavail {
				log.Info("Pod blocked due to local PVC being unreachable",
					"pod", pod, "pvc", pvcName, "node", node)
			}

			// If no other recovery is tracked by the mariadb-operator
			// process this PVC to prepare recovery
			if _, inProgress := instance.Status.RemediationInProgress(); !inProgress {
				desc.Remediation = true
			}

			instance.Status.UnavailablePods[pod] = desc
		}
	}

	// At this point, all pods currently unavailable have been tagged,
	// So check whether a remediation action must be initiated or tracked.
	if err := r.CheckForPendingPodRemediation(ctx, instance, helper, availableReplicas); err != nil {
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
	availableReplicas int32,
) error {
	log := helper.GetLogger()

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

	// If the mariadb-operator annotated the PVC for the PodRemediator,
	// nothing left to be done until the pod restarts and its local
	// galera is synced
	if pvc.Annotations[remediationv1.SafeToDeleteAnnotation] == "true" {
		return nil
	}
	if instance.Spec.Replicas == nil || *instance.Spec.Replicas == 0 {
		return nil
	}

	requiredQuorum := *instance.Spec.Replicas/2 + 1
	if availableReplicas < requiredQuorum {
		// Deleting a stuck PVC without a surviving Galera majority can remove
		// the recovery path for the data that remains in the cluster.
		log.Info("Deferring PVC remediation until Galera quorum is available",
			"pod", podName, "pvc", pvcName,
			"availableReplicas", availableReplicas,
			"requiredReplicas", requiredQuorum)
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
//     pod is ready and galera status is "synced", and if so clear the
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
	if !isGaleraRunningAndSynced(ctx, pod, instance, helper, r.config) {
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
