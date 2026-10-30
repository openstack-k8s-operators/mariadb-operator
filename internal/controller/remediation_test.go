package controller

import (
	"context"
	"testing"

	remediationv1 "github.com/openstack-k8s-operators/infra-operator/apis/remediation/v1beta1"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// remTestNS and remTestName define the namespace and Galera CR name shared
// across all remediation tests. Derived names follow the operator conventions:
//
//   StatefulSet : remTestName + "-galera"   = "test-galera"
//   PVC ordinal0: "mysql-db-test-galera-0"
//   pod ordinal0: "test-galera-0"

const (
	remTestNS   = "test-ns"
	remTestName = "test"
)

// remPodName returns the Galera pod name for ordinal i using the test CR name.
func remPodName(i int32) string {
	return mariadb.PodNameFromPVCName(mariadb.PVCName(mariadb.StatefulSetName(remTestName), i))
}

// remPVCName returns the PVC name for ordinal i using the test CR name.
func remPVCName(i int32) string {
	return mariadb.PVCName(mariadb.StatefulSetName(remTestName), i)
}

// remScheme returns a scheme with corev1 and mariadbv1 registered, the minimum
// required for fake-client tests that also call helper.NewHelper.
func remScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1.AddToScheme: %v", err)
	}
	if err := mariadbv1.AddToScheme(s); err != nil {
		t.Fatalf("mariadbv1.AddToScheme: %v", err)
	}
	return s
}

// remSetup builds a fake-client GaleraReconciler and a matching helper.Helper
// sharing the same client and scheme. Pre-created Kubernetes objects (PVCs,
// Pods, etc.) can be passed as objs; they will be visible via r.Get().
func remSetup(t *testing.T, instance *mariadbv1.Galera, objs ...client.Object) (*GaleraReconciler, *helper.Helper) {
	t.Helper()
	s := remScheme(t)
	c := clientfake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	r := &GaleraReconciler{Client: c}
	h, err := helper.NewHelper(instance, c, k8sfake.NewSimpleClientset(), s, ctrl.Log)
	if err != nil {
		t.Fatalf("helper.NewHelper: %v", err)
	}
	return r, h
}

// stuckPVC returns a PVC carrying the PodRemediator stuck-on-node annotation
// and, when requestID is non-empty, the request-id annotation too.
func stuckPVC(name, ns, node, requestID string) *corev1.PersistentVolumeClaim {
	ann := map[string]string{
		remediationv1.PVCStuckOnNodeAnnotation: node,
	}
	if requestID != "" {
		ann[remediationv1.RequestIDAnnotation] = requestID
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: ann},
	}
}

// cleanPVC returns a PVC with no remediation annotations.
func cleanPVC(name, ns string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

// pvcWithConsent returns a PVC that already carries safe-to-delete=true,
// simulating a state where the operator has already granted consent.
func pvcWithConsent(name, ns, node, requestID string) *corev1.PersistentVolumeClaim {
	pvc := stuckPVC(name, ns, node, requestID)
	pvc.Annotations[remediationv1.SafeToDeleteAnnotation] = "true"
	pvc.Annotations[remediationv1.ConsentIDAnnotation] = requestID
	return pvc
}

// unavailable builds an UnavailablePodStatus for a pod stuck due to a local PVC.
func unavailable(remediation bool) mariadbv1.UnavailablePodStatus {
	return mariadbv1.UnavailablePodStatus{
		Reason:      mariadbv1.PodUnavailabilityReasonPVCStuck,
		Node:        "worker-0",
		Remediation: remediation,
	}
}

//
// Mariadb remediation tests below
//

func TestRemediationInProgress_Nil(t *testing.T) {
	// nil UnavailablePods means the cluster is fully healthy; no recovery active.
	s := &mariadbv1.GaleraStatus{}
	name, ok := s.RemediationInProgress()
	if ok || name != "" {
		t.Errorf("expected (\"\", false), got (%q, %v)", name, ok)
	}
}

func TestRemediationInProgress_Empty(t *testing.T) {
	// An empty (non-nil) UnavailablePods map also means no recovery is active.
	s := &mariadbv1.GaleraStatus{
		UnavailablePods: map[string]mariadbv1.UnavailablePodStatus{},
	}
	name, ok := s.RemediationInProgress()
	if ok || name != "" {
		t.Errorf("expected (\"\", false), got (%q, %v)", name, ok)
	}
}

func TestRemediationInProgress_NoneActive(t *testing.T) {
	// A pod tracked in UnavailablePods with Remediation=false is queued but
	// not yet being remediated; RemediationInProgress must return false.
	s := &mariadbv1.GaleraStatus{
		UnavailablePods: map[string]mariadbv1.UnavailablePodStatus{
			"pod-0": {Reason: mariadbv1.PodUnavailabilityReasonPVCStuck, Remediation: false},
		},
	}
	name, ok := s.RemediationInProgress()
	if ok || name != "" {
		t.Errorf("expected (\"\", false), got (%q, %v)", name, ok)
	}
}

func TestRemediationInProgress_OneActive(t *testing.T) {
	// Exactly one pod has Remediation=true; RemediationInProgress must return
	// that pod's name and true.
	s := &mariadbv1.GaleraStatus{
		UnavailablePods: map[string]mariadbv1.UnavailablePodStatus{
			"pod-0": {Reason: mariadbv1.PodUnavailabilityReasonPVCStuck, Remediation: true},
		},
	}
	name, ok := s.RemediationInProgress()
	if !ok || name != "pod-0" {
		t.Errorf("expected (\"pod-0\", true), got (%q, %v)", name, ok)
	}
}

func TestRemediationInProgress_MultiplePodsOneActive(t *testing.T) {
	// Two pods are stuck, but only one is actively being remediated. The method
	// must return the name of the active one and not the queued one.
	s := &mariadbv1.GaleraStatus{
		UnavailablePods: map[string]mariadbv1.UnavailablePodStatus{
			"pod-0": {Reason: mariadbv1.PodUnavailabilityReasonPVCStuck, Remediation: true},
			"pod-1": {Reason: mariadbv1.PodUnavailabilityReasonPVCStuck, Remediation: false},
		},
	}
	name, ok := s.RemediationInProgress()
	if !ok || name != "pod-0" {
		t.Errorf("expected (\"pod-0\", true), got (%q, %v)", name, ok)
	}
}

func TestCheckForPendingPodRemediation_NoRemediationInProgress(t *testing.T) {
	// When no pod has Remediation=true, the function must return immediately
	// without touching any PVC.
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	// No UnavailablePods -> RemediationInProgress returns false.
	r, h := remSetup(t, instance)
	if err := r.CheckForPendingPodRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckForPendingPodRemediation_PVCAlreadyDeleted(t *testing.T) {
	// The PVC is gone (deleted by PodRemediator or already rescheduled).
	// The function should return nil without error; the pod will resync later.
	pod0 := remPodName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(true),
	}
	// No PVC object registered in the fake client.
	r, h := remSetup(t, instance)
	if err := r.CheckForPendingPodRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckForPendingPodRemediation_StuckAnnotationGone(t *testing.T) {
	// The PVC exists but the pvc-stuck-on-node annotation has been removed
	// (e.g. the node recovered). No consent should be granted.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(true),
	}
	r, h := remSetup(t, instance, cleanPVC(pvc0, remTestNS))

	if err := r.CheckForPendingPodRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// safe-to-delete must not have been set.
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: pvc0, Namespace: remTestNS}, pvc); err != nil {
		t.Fatalf("Get PVC: %v", err)
	}
	if pvc.Annotations[remediationv1.SafeToDeleteAnnotation] == "true" {
		t.Error("safe-to-delete must not be set when the stuck annotation is absent")
	}
}

func TestCheckForPendingPodRemediation_NoRequestID_Defers(t *testing.T) {
	// The PVC is stuck but PodRemediator has not yet written a request-id.
	// Consent must not be granted until a request-id is present -- an empty
	// request-id would match any future handshake and could authorize an
	// unrelated deletion.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(true),
	}
	// stuckPVC with empty requestID -> no request-id annotation.
	r, h := remSetup(t, instance, stuckPVC(pvc0, remTestNS, "worker-0", ""))

	if err := r.CheckForPendingPodRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: pvc0, Namespace: remTestNS}, pvc); err != nil {
		t.Fatalf("Get PVC: %v", err)
	}
	if pvc.Annotations[remediationv1.SafeToDeleteAnnotation] == "true" {
		t.Error("safe-to-delete must not be set before a request-id is present")
	}
}

func TestCheckForPendingPodRemediation_GrantsConsent(t *testing.T) {
	// The PVC is stuck and PodRemediator has written a request-id. The operator
	// must patch safe-to-delete=true and echo back the request-id as consent-id
	// in a single atomic update so PodRemediator's HasRemediationConsent check passes.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	requestID := "req-abc-123"
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(true),
	}
	r, h := remSetup(t, instance, stuckPVC(pvc0, remTestNS, "worker-0", requestID))

	if err := r.CheckForPendingPodRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: pvc0, Namespace: remTestNS}, pvc); err != nil {
		t.Fatalf("Get PVC: %v", err)
	}
	if got := pvc.Annotations[remediationv1.SafeToDeleteAnnotation]; got != "true" {
		t.Errorf("safe-to-delete: want \"true\", got %q", got)
	}
	if got := pvc.Annotations[remediationv1.ConsentIDAnnotation]; got != requestID {
		t.Errorf("consent-id: want %q, got %q", requestID, got)
	}
}

func TestCheckForPendingPodRemediation_AlreadyConsented(t *testing.T) {
	// safe-to-delete=true is already set. The function must return without
	// issuing another patch (idempotent): a second patch could race with
	// PodRemediator updating the request-id for a new fault event.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	requestID := "req-xyz-456"
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(true),
	}
	r, h := remSetup(t, instance, pvcWithConsent(pvc0, remTestNS, "worker-0", requestID))

	if err := r.CheckForPendingPodRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The annotations must remain unchanged.
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: pvc0, Namespace: remTestNS}, pvc); err != nil {
		t.Fatalf("Get PVC: %v", err)
	}
	if got := pvc.Annotations[remediationv1.SafeToDeleteAnnotation]; got != "true" {
		t.Errorf("safe-to-delete: want \"true\", got %q", got)
	}
	if got := pvc.Annotations[remediationv1.ConsentIDAnnotation]; got != requestID {
		t.Errorf("consent-id: want %q, got %q", requestID, got)
	}
}

// Note: EnsurePVCAvailability() calls CheckForResolvedPodRemediation first, which
// would exec into a pod if one is being remediated. To keep these tests exec-free,
// cases that start with an active remediation (Remediation=true) omit the pod
// from the fake client so that CheckForResolvedPodRemediation returns early
// with a not-found result.

func TestEnsurePVCAvailability_NoPVCs(t *testing.T) {
	// When the StatefulSet has not yet created any PVCs, UnavailablePods must
	// remain nil after the scan.
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	r, h := remSetup(t, instance)

	if err := r.EnsurePVCAvailability(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instance.Status.UnavailablePods != nil {
		t.Errorf("expected nil UnavailablePods, got %v", instance.Status.UnavailablePods)
	}
}

func TestEnsurePVCAvailability_CleanPVC_NotTracked(t *testing.T) {
	// A PVC that carries no stuck annotation must not cause its pod to appear
	// in UnavailablePods.
	pvc0 := remPVCName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	r, h := remSetup(t, instance, cleanPVC(pvc0, remTestNS))

	if err := r.EnsurePVCAvailability(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instance.Status.UnavailablePods != nil {
		t.Errorf("expected nil UnavailablePods, got %v", instance.Status.UnavailablePods)
	}
}

func TestEnsurePVCAvailability_FirstDetection_StartsRemediation(t *testing.T) {
	// When a PVC is annotated stuck for the first time and no other remediation
	// is active, the associated pod must be added to UnavailablePods with
	// Remediation=true so the consent handshake begins.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	r, h := remSetup(t, instance, stuckPVC(pvc0, remTestNS, "worker-0", "req-1"))

	if err := r.EnsurePVCAvailability(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status, ok := instance.Status.UnavailablePods[pod0]
	if !ok {
		t.Fatalf("expected pod %q in UnavailablePods", pod0)
	}
	if !status.Remediation {
		t.Error("Remediation must be true when no other remediation is in progress")
	}
	if status.Reason != mariadbv1.PodUnavailabilityReasonPVCStuck {
		t.Errorf("Reason: want PVCStuck, got %v", status.Reason)
	}
}

func TestEnsurePVCAvailability_TwoStuckPVCs_Serialized(t *testing.T) {
	// Two PVCs are stuck simultaneously. Only the first one (ordinal 0) must
	// get Remediation=true; the second (ordinal 1) must queue with
	// Remediation=false. Serialization prevents deleting all PVCs at once,
	// which would destroy the entire dataset.
	pod0 := remPodName(0)
	pod1 := remPodName(1)
	pvc0 := remPVCName(0)
	pvc1 := remPVCName(1)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	r, h := remSetup(t, instance,
		stuckPVC(pvc0, remTestNS, "worker-0", "req-1"),
		stuckPVC(pvc1, remTestNS, "worker-1", "req-2"),
	)

	if err := r.EnsurePVCAvailability(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s0, ok0 := instance.Status.UnavailablePods[pod0]
	s1, ok1 := instance.Status.UnavailablePods[pod1]
	if !ok0 || !ok1 {
		t.Fatalf("expected both pods in UnavailablePods, got %v", instance.Status.UnavailablePods)
	}
	if !s0.Remediation {
		t.Errorf("pod0 (ordinal 0): expected Remediation=true")
	}
	if s1.Remediation {
		t.Errorf("pod1 (ordinal 1): expected Remediation=false while pod0 is being remediated")
	}
}

func TestEnsurePVCAvailability_AnnotationCleared_NoActiveRemediation(t *testing.T) {
	// A pod was tracked in UnavailablePods (Remediation=false, queued) but its
	// PVC annotation has since been removed (e.g. node recovered). The entry
	// must be cleared because the pod is no longer stuck.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	// Pod0 was queued but no active remediation is in progress.
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(false),
	}
	r, h := remSetup(t, instance, cleanPVC(pvc0, remTestNS))

	if err := r.EnsurePVCAvailability(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, still := instance.Status.UnavailablePods[pod0]; still {
		t.Errorf("expected %q to be removed from UnavailablePods after annotation cleared", pod0)
	}
}

func TestEnsurePVCAvailability_AnnotationCleared_ActiveRemediation_Preserved(t *testing.T) {
	// The stuck annotation has been removed, but the pod is currently being
	// remediated (consent was already granted). The UnavailablePods entry must
	// NOT be cleared until CheckForResolvedPodRemediation confirms the pod's
	// galera is Primary and Synced -- clearing it early would free the
	// remediation slot before the data is safe.
	//
	// Because the pod does not exist in the fake client,
	// CheckForResolvedPodRemediation returns early (not-found -> no-op),
	// leaving the entry intact for this test to verify.
	pod0 := remPodName(0)
	pvc0 := remPVCName(0)
	instance := makeGaleraNamed(remTestNS, remTestName, 3, nil)
	// Pod0 has active remediation (consent already granted).
	instance.Status.UnavailablePods = map[string]mariadbv1.UnavailablePodStatus{
		pod0: unavailable(true),
	}
	// PVC exists but no longer has the stuck annotation (PodRemediator removed it).
	r, h := remSetup(t, instance, cleanPVC(pvc0, remTestNS))

	if err := r.EnsurePVCAvailability(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, still := instance.Status.UnavailablePods[pod0]; !still {
		t.Errorf("entry for %q must be preserved until the pod fully resyncs", pod0)
	}
}
