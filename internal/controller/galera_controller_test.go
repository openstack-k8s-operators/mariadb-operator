package controller

import (
	"testing"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func int32Ptr(i int32) *int32 { return &i }

func makeGalera(replicas int32, attrs map[string]mariadbv1.GaleraAttributes) *mariadbv1.Galera {
	return makeGaleraNamed("test-ns", "galera", replicas, attrs)
}

func makeGaleraNamed(ns, name string, replicas int32, attrs map[string]mariadbv1.GaleraAttributes) *mariadbv1.Galera {
	return &mariadbv1.Galera{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: mariadbv1.GaleraSpec{
			GaleraSpecCore: mariadbv1.GaleraSpecCore{
				Replicas: int32Ptr(replicas),
			},
		},
		Status: mariadbv1.GaleraStatus{
			Attributes: attrs,
		},
	}
}

func makePod(name, containerID string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "galera", ContainerID: containerID},
			},
		},
	}
}

func TestFindBestCandidate_AllFreshCIDs(t *testing.T) {
	g := makeGalera(3, map[string]mariadbv1.GaleraAttributes{
		"galera-0": {Seqno: "100", ContainerID: "cid-0"},
		"galera-1": {Seqno: "100", ContainerID: "cid-1"},
		"galera-2": {Seqno: "101", ContainerID: "cid-2"},
	})
	pods := []corev1.Pod{
		makePod("galera-0", "cid-0"),
		makePod("galera-1", "cid-1"),
		makePod("galera-2", "cid-2"),
	}
	node, found := findBestCandidate(g, pods, ctrl.Log)
	if !found {
		t.Fatal("expected to find a candidate")
	}
	if node != "galera-2" {
		t.Errorf("expected galera-2 (highest seqno), got %s", node)
	}
}

func TestFindBestCandidate_SafeToBootstrap(t *testing.T) {
	g := makeGalera(3, map[string]mariadbv1.GaleraAttributes{
		"galera-0": {Seqno: "100", ContainerID: "cid-0", SafeToBootstrap: true},
		"galera-1": {Seqno: "200", ContainerID: "cid-1"},
		"galera-2": {Seqno: "200", ContainerID: "cid-2"},
	})
	pods := []corev1.Pod{
		makePod("galera-0", "cid-0"),
		makePod("galera-1", "cid-1"),
		makePod("galera-2", "cid-2"),
	}
	node, found := findBestCandidate(g, pods, ctrl.Log)
	if !found {
		t.Fatal("expected to find a candidate")
	}
	if node != "galera-0" {
		t.Errorf("expected galera-0 (SafeToBootstrap), got %s", node)
	}
}

func TestFindBestCandidate_StaleCIDs_StillWorks(t *testing.T) {
	// Pods have restarted and have new CIDs, but attributes still have
	// old CIDs from a previous push. findBestCandidate should NOT care
	// about CID freshness -- it only needs seqno data from all replicas.
	g := makeGalera(3, map[string]mariadbv1.GaleraAttributes{
		"galera-0": {Seqno: "100", ContainerID: "old-cid-0"},
		"galera-1": {Seqno: "100", ContainerID: "old-cid-1"},
		"galera-2": {Seqno: "100", ContainerID: "old-cid-2"},
	})
	pods := []corev1.Pod{
		makePod("galera-0", "new-cid-0"),
		makePod("galera-1", "new-cid-1"),
		makePod("galera-2", "new-cid-2"),
	}
	node, found := findBestCandidate(g, pods, ctrl.Log)
	if !found {
		t.Fatal("expected to find a candidate even with stale CIDs")
	}
	t.Logf("Selected node: %s", node)
}

func TestFindBestCandidate_NotAllReported(t *testing.T) {
	// Only 2 of 3 replicas have pushed attributes
	g := makeGalera(3, map[string]mariadbv1.GaleraAttributes{
		"galera-0": {Seqno: "100", ContainerID: "cid-0"},
		"galera-1": {Seqno: "100", ContainerID: "cid-1"},
	})
	pods := []corev1.Pod{
		makePod("galera-0", "cid-0"),
		makePod("galera-1", "cid-1"),
		makePod("galera-2", "cid-2"),
	}
	_, found := findBestCandidate(g, pods, ctrl.Log)
	if found {
		t.Error("should not find candidate when not all replicas have reported")
	}
}

func TestFindBestCandidate_UnequalSeqno(t *testing.T) {
	g := makeGalera(3, map[string]mariadbv1.GaleraAttributes{
		"galera-0": {Seqno: "100", ContainerID: "cid-0"},
		"galera-1": {Seqno: "200", ContainerID: "cid-1"},
		"galera-2": {Seqno: "150", ContainerID: "cid-2"},
	})
	pods := []corev1.Pod{
		makePod("galera-0", "cid-0"),
		makePod("galera-1", "cid-1"),
		makePod("galera-2", "cid-2"),
	}
	node, found := findBestCandidate(g, pods, ctrl.Log)
	if !found {
		t.Fatal("expected to find a candidate")
	}
	if node != "galera-1" {
		t.Errorf("expected galera-1 (highest seqno=200), got %s", node)
	}
}

// Reproduces the exact scenario from the live failure
func TestFindBestCandidate_LiveScenario_AllEqualSeqno(t *testing.T) {
	g := makeGalera(3, map[string]mariadbv1.GaleraAttributes{
		"openstack-cell1-galera-0": {
			UUID:        "918a5168-7773-11f1-bce9-4ad1e2e8f877",
			Seqno:       "1892",
			ContainerID: "cri-o://4c72f596",
		},
		"openstack-cell1-galera-1": {
			UUID:        "918a5168-7773-11f1-bce9-4ad1e2e8f877",
			Seqno:       "1892",
			ContainerID: "cri-o://90a8fb82",
		},
		"openstack-cell1-galera-2": {
			UUID:        "918a5168-7773-11f1-bce9-4ad1e2e8f877",
			Seqno:       "1892",
			ContainerID: "cri-o://7cd39428",
		},
	})
	// Pods have DIFFERENT CIDs (restarted since pushing)
	pods := []corev1.Pod{
		makePod("openstack-cell1-galera-0", "cri-o://NEW-0"),
		makePod("openstack-cell1-galera-1", "cri-o://NEW-1"),
		makePod("openstack-cell1-galera-2", "cri-o://NEW-2"),
	}
	node, found := findBestCandidate(g, pods, ctrl.Log)
	if !found {
		t.Fatal("expected to find a candidate despite CID mismatches")
	}
	t.Logf("Selected node: %s", node)
}

func TestIsBootstrapInProgress_NoState(t *testing.T) {
	r := &GaleraReconciler{}
	g := makeGaleraNamed("ns", "galera", 3, nil)
	pods := []corev1.Pod{makePod("galera-0", "cid-0")}
	if r.isBootstrapInProgress(g, pods) {
		t.Error("expected false when no bootstrap state exists")
	}
}

func TestIsBootstrapInProgress_SameCID(t *testing.T) {
	r := &GaleraReconciler{}
	g := makeGaleraNamed("ns", "galera", 3, nil)
	r.setBootstrapInProgress(g, "galera-0", "cid-0")

	pods := []corev1.Pod{makePod("galera-0", "cid-0")}
	if !r.isBootstrapInProgress(g, pods) {
		t.Error("expected true when bootstrap pod is still running with same CID")
	}
}

func TestIsBootstrapInProgress_PodRestarted(t *testing.T) {
	r := &GaleraReconciler{}
	g := makeGaleraNamed("ns", "galera", 3, nil)
	r.setBootstrapInProgress(g, "galera-0", "old-cid")

	pods := []corev1.Pod{makePod("galera-0", "new-cid")}
	if r.isBootstrapInProgress(g, pods) {
		t.Error("expected false when bootstrap pod has a new CID (restarted)")
	}
	// State should have been cleared
	if r.isBootstrapInProgress(g, pods) {
		t.Error("expected state to be cleared after CID mismatch")
	}
}

func TestIsBootstrapInProgress_PodGone(t *testing.T) {
	r := &GaleraReconciler{}
	g := makeGaleraNamed("ns", "galera", 3, nil)
	r.setBootstrapInProgress(g, "galera-0", "cid-0")

	// Pod list does not contain galera-0
	pods := []corev1.Pod{makePod("galera-1", "cid-1")}
	if r.isBootstrapInProgress(g, pods) {
		t.Error("expected false when bootstrap pod is no longer in pod list")
	}
	// State should have been cleared
	if r.isBootstrapInProgress(g, pods) {
		t.Error("expected state to be cleared after pod disappeared")
	}
}

func TestClearBootstrapState(t *testing.T) {
	r := &GaleraReconciler{}
	g := makeGaleraNamed("ns", "galera", 3, nil)
	r.setBootstrapInProgress(g, "galera-0", "cid-0")

	pods := []corev1.Pod{makePod("galera-0", "cid-0")}
	if !r.isBootstrapInProgress(g, pods) {
		t.Fatal("precondition: bootstrap should be in progress")
	}

	r.clearBootstrapState(g)
	if r.isBootstrapInProgress(g, pods) {
		t.Error("expected false after clearBootstrapState")
	}
}

func TestBootstrapState_MultipleInstances(t *testing.T) {
	r := &GaleraReconciler{}
	g1 := makeGaleraNamed("ns", "cell1-galera", 3, nil)
	g2 := makeGaleraNamed("ns", "cell2-galera", 3, nil)

	r.setBootstrapInProgress(g1, "cell1-galera-0", "cid-1")

	pods1 := []corev1.Pod{makePod("cell1-galera-0", "cid-1")}
	pods2 := []corev1.Pod{makePod("cell2-galera-0", "cid-2")}

	if !r.isBootstrapInProgress(g1, pods1) {
		t.Error("expected true for cell1")
	}
	if r.isBootstrapInProgress(g2, pods2) {
		t.Error("expected false for cell2 (no bootstrap set)")
	}

	// Setting bootstrap for cell2 should not affect cell1
	r.setBootstrapInProgress(g2, "cell2-galera-0", "cid-2")
	if !r.isBootstrapInProgress(g1, pods1) {
		t.Error("cell1 bootstrap should still be in progress")
	}
	if !r.isBootstrapInProgress(g2, pods2) {
		t.Error("cell2 bootstrap should now be in progress")
	}

	// Clearing cell1 should not affect cell2
	r.clearBootstrapState(g1)
	if r.isBootstrapInProgress(g1, pods1) {
		t.Error("cell1 should be cleared")
	}
	if !r.isBootstrapInProgress(g2, pods2) {
		t.Error("cell2 should still be in progress")
	}
}
