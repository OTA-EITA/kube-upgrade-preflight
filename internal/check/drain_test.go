package check

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func drainNodeObj(name, cpu, mem string, controlPlane bool) *corev1.Node {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.34.1"},
		},
	}
	if controlPlane {
		n.Labels[controlPlaneLabel] = ""
		n.Spec.Taints = []corev1.Taint{{Key: controlPlaneLabel, Effect: corev1.TaintEffectNoSchedule}}
	}
	return n
}

// ownedPod creates a running pod. ownerKind "" makes an unmanaged pod.
func ownedPod(name, node, ownerKind, cpu, mem string, labels map[string]string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: name, Labels: labels},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			}}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if ownerKind != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: name + "-owner", Controller: ptr.To(true)}}
	}
	return p
}

func scenario() []runtime.Object {
	db := ownedPod("db-0", "w1", "StatefulSet", "1", "2Gi", map[string]string{"app": "db"})
	db.Spec.Volumes = []corev1.Volume{{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	mirror := ownedPod("etcd-cp", "cp", "", "100m", "100Mi", nil)
	mirror.Annotations = map[string]string{mirrorPodAnnotation: "x"}

	return []runtime.Object{
		drainNodeObj("cp", "2", "4Gi", true),
		drainNodeObj("w1", "2", "4Gi", false),
		drainNodeObj("w2", "2", "4Gi", false),
		mirror,
		ownedPod("api-1", "w1", "ReplicaSet", "500m", "1Gi", map[string]string{"app": "api"}),
		ownedPod("api-2", "w2", "ReplicaSet", "500m", "1Gi", map[string]string{"app": "api"}),
		db,
		ownedPod("debug", "w1", "", "100m", "64Mi", nil),
		ownedPod("node-exporter-w1", "w1", "DaemonSet", "100m", "64Mi", nil),
		pdb("app", "api", map[string]string{"app": "api"}, 1, 2, 1, 2),
		pdb("app", "db", map[string]string{"app": "db"}, 0, 1, 1, 1),
	}
}

func TestDrainSimulation(t *testing.T) {
	cs := newFake("v1.34.1", scenario()...)
	fs := runCheck(t, DrainSimulation{}, Cluster{Client: cs}, testOptions("v1.35"))

	// The control plane only runs a static pod, which drain leaves alone.
	if f := expectStatus(t, fs, "node/cp", StatusPass); f.Message != "0 pod(s) can be evicted and rescheduled" {
		t.Errorf("cp: %q", f.Message)
	}

	w1 := findingsFor(fs, "node/w1")
	mustContain(t, w1, StatusFail, "pod app/debug has no controller")
	mustContain(t, w1, StatusWarn, "pod app/db-0 uses emptyDir")
	mustContain(t, w1, StatusFail, "PDB app/db allows 0 disruptions")
	for _, f := range w1 {
		if strings.Contains(f.Message, "cannot reschedule") || strings.Contains(f.Message, "node-exporter") {
			t.Errorf("w1: unexpected finding %+v", f)
		}
	}

	// db-0 moved to w2 while w1 was drained, so draining w2 must evict it again.
	w2 := findingsFor(fs, "node/w2")
	mustContain(t, w2, StatusFail, "PDB app/db allows 0 disruptions")
	mustContain(t, w2, StatusWarn, "pod app/db-0 uses emptyDir")
}

func TestDrainSimulationReportsMissingCapacity(t *testing.T) {
	cs := newFake("v1.34.1",
		drainNodeObj("cp", "4", "8Gi", true),
		drainNodeObj("w1", "2", "4Gi", false),
		drainNodeObj("w2", "2", "4Gi", false),
		ownedPod("big-1", "w1", "ReplicaSet", "1500m", "3Gi", nil),
		ownedPod("big-2", "w2", "ReplicaSet", "1500m", "3Gi", nil),
	)
	fs := runCheck(t, DrainSimulation{}, Cluster{Client: cs}, testOptions("v1.35"))
	f := expectStatus(t, fs, "node/w1", StatusFail)
	for _, want := range []string{"cannot reschedule", "app/big-1", "1 untolerated taint node-role.kubernetes.io/control-plane", "1 insufficient cpu"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q should contain %q", f.Message, want)
		}
	}
}

func TestDrainSimulationServerDryRun(t *testing.T) {
	cs := newFake("v1.34.1",
		drainNodeObj("w1", "2", "4Gi", false),
		drainNodeObj("w2", "2", "4Gi", false),
		ownedPod("api-1", "w1", "ReplicaSet", "100m", "64Mi", nil),
		ownedPod("db-0", "w1", "StatefulSet", "100m", "64Mi", nil),
	)
	var dryRuns []string
	cs.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		ev := a.(k8stesting.CreateAction).GetObject().(interface{ GetName() string })
		dryRuns = append(dryRuns, ev.GetName())
		if ev.GetName() == "db-0" {
			return true, nil, apierrors.NewTooManyRequests("Cannot evict pod as it would violate the pod's disruption budget.", 10)
		}
		return true, nil, nil
	})

	opt := testOptions("v1.35")
	opt.ServerDryRun = true
	fs := runCheck(t, DrainSimulation{}, Cluster{Client: cs}, opt)
	mustContain(t, findingsFor(fs, "node/w1"), StatusFail, "API server rejected dry-run eviction of app/db-0")
	if len(dryRuns) < 2 {
		t.Fatalf("expected dry-run evictions for both pods, got %v", dryRuns)
	}

	// Without the flag the Eviction API must never be called.
	dryRuns = nil
	runCheck(t, DrainSimulation{}, Cluster{Client: cs}, testOptions("v1.35"))
	if len(dryRuns) != 0 {
		t.Fatalf("eviction API called without --server-dry-run: %v", dryRuns)
	}
}

func TestMatchingPDBs(t *testing.T) {
	p := ownedPod("api-1", "w1", "ReplicaSet", "1", "1Gi", map[string]string{"app": "api"})
	nilSel := pdb("app", "nil", nil, 1, 1, 1, 1)
	nilSel.Spec.Selector = nil
	empty := pdb("app", "all", nil, 1, 1, 1, 1)
	other := pdb("other", "api", map[string]string{"app": "api"}, 1, 1, 1, 1)
	got := matchingPDBs(p, []policyv1.PodDisruptionBudget{*nilSel, *empty, *other, *pdb("app", "api", map[string]string{"app": "api"}, 1, 1, 1, 1)})
	if len(got) != 2 || got[0].Name != "all" || got[1].Name != "api" {
		t.Fatalf("got %v", got)
	}
}

func mustContain(t *testing.T, fs []Finding, status Status, substr string) {
	t.Helper()
	for _, f := range fs {
		if f.Status == status && strings.Contains(f.Message, substr) {
			return
		}
	}
	t.Errorf("no %s finding containing %q in %+v", status, substr, fs)
}
