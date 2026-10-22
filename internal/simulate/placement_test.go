package simulate

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testNode(name, cpu, mem string, labels map[string]string, taints ...corev1.Taint) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Taints: taints},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func testPod(name, node, cpu, mem string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: name},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name: "app",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse(mem),
				}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestRequestsIncludesInitContainers(t *testing.T) {
	p := testPod("a", "", "100m", "128Mi")
	p.Spec.InitContainers = []corev1.Container{{
		Name:      "migrate",
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}},
	}}
	got := Requests(&p)
	if got.MilliCPU != 100 || got.Memory != 1<<30 || got.Pods != 1 {
		t.Fatalf("Requests = %+v; init container memory should dominate", got)
	}
}

func TestPlacePrefersMostFreeMemory(t *testing.T) {
	c := New(
		[]corev1.Node{testNode("a", "4", "8Gi", nil), testNode("b", "4", "8Gi", nil)},
		[]corev1.Pod{testPod("big", "a", "1", "6Gi")},
	)
	p := testPod("new", "", "500m", "1Gi")
	node, reason := c.Place(&p)
	if node != "b" || reason != "" {
		t.Fatalf("Place = %q, %q; want b", node, reason)
	}
}

func TestPlaceReservesResources(t *testing.T) {
	c := New([]corev1.Node{testNode("a", "1", "2Gi", nil)}, nil)
	first := testPod("x", "", "600m", "1Gi")
	second := testPod("y", "", "600m", "1Gi")
	if node, _ := c.Place(&first); node != "a" {
		t.Fatal("first pod should fit")
	}
	if node, reason := c.Place(&second); node != "" || !strings.Contains(reason, "insufficient cpu") {
		t.Fatalf("second pod should not fit: %q %q", node, reason)
	}
}

func TestPlaceExplainsWhyNothingFits(t *testing.T) {
	gpu := map[string]string{"accelerator": "gpu"}
	c := New([]corev1.Node{
		testNode("cordoned", "8", "16Gi", nil),
		testNode("tainted", "8", "16Gi", nil, corev1.Taint{Key: "dedicated", Value: "db", Effect: corev1.TaintEffectNoSchedule}),
		testNode("small", "100m", "64Mi", nil),
		testNode("gpu", "8", "16Gi", gpu),
	}, nil)
	c.Cordon("cordoned")

	p := testPod("api", "", "500m", "512Mi")
	p.Spec.NodeSelector = map[string]string{"pool": "general"}
	_, reason := c.Place(&p)
	for _, want := range []string{"0/4 nodes are available", "1 cordoned", "3 node selector/affinity mismatch"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should contain %q", reason, want)
		}
	}

	huge := testPod("batch", "", "9", "1Gi")
	_, reason = c.Place(&huge)
	for _, want := range []string{"1 cordoned", "1 untolerated taint dedicated", "2 insufficient cpu"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should contain %q", reason, want)
		}
	}

	// Without the selector the pod still cannot use the tainted or small nodes,
	// but it can use the gpu node.
	p.Spec.NodeSelector = nil
	if node, _ := c.Place(&p); node != "gpu" {
		t.Fatalf("got %q, want gpu", node)
	}
}

func TestPlaceHonoursTolerationsAndUncordon(t *testing.T) {
	c := New([]corev1.Node{
		testNode("db", "4", "8Gi", nil, corev1.Taint{Key: "dedicated", Value: "db", Effect: corev1.TaintEffectNoSchedule}),
	}, nil)
	p := testPod("postgres", "", "1", "2Gi")
	p.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "db", Effect: corev1.TaintEffectNoSchedule}}

	c.Cordon("db")
	if node, _ := c.Place(&p); node != "" {
		t.Fatal("cordoned node must not be used")
	}
	c.Uncordon("db")
	if node, _ := c.Place(&p); node != "db" {
		t.Fatal("tolerated taint should allow scheduling after uncordon")
	}
}

func TestNewIgnoresTerminatedPodsAndRelease(t *testing.T) {
	done := testPod("job", "a", "1", "1Gi")
	done.Status.Phase = corev1.PodSucceeded
	running := testPod("api", "a", "1", "1Gi")
	c := New([]corev1.Node{testNode("a", "1", "1Gi", nil)}, []corev1.Pod{done, running})

	p := testPod("new", "", "1", "1Gi")
	if node, _ := c.Place(&p); node != "" {
		t.Fatal("node is full with the running pod")
	}
	c.Release("a", &running)
	if node, _ := c.Place(&p); node != "a" {
		t.Fatal("released resources should be reusable")
	}
}

func TestNotReadyNodeIsSkipped(t *testing.T) {
	n := testNode("a", "4", "8Gi", nil)
	n.Status.Conditions[0].Status = corev1.ConditionFalse
	c := New([]corev1.Node{n}, nil)
	p := testPod("x", "", "100m", "64Mi")
	if _, reason := c.Place(&p); !strings.Contains(reason, "not ready") {
		t.Fatalf("reason = %q", reason)
	}
}
