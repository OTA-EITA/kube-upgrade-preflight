package simulate

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// spreadPod returns a pod that refuses to share a topology domain with pods labelled app=<app>.
func spreadPod(name, node, app, topologyKey string) corev1.Pod {
	p := testPod(name, node, "100m", "64Mi")
	p.Labels = map[string]string{"app": app}
	p.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}},
			TopologyKey:   topologyKey,
		}},
	}}
	return p
}

func TestAntiAffinityOnePerNode(t *testing.T) {
	c := New(
		[]corev1.Node{testNode("a", "4", "8Gi", nil), testNode("b", "4", "8Gi", nil), testNode("c", "4", "8Gi", nil)},
		[]corev1.Pod{
			spreadPod("web-1", "a", "web", hostnameLabel),
			spreadPod("web-2", "b", "web", hostnameLabel),
			spreadPod("web-3", "c", "web", hostnameLabel),
		},
	)
	// Drain c: web-3 has nowhere to go because a and b already run a web pod.
	evicted := spreadPod("web-3", "c", "web", hostnameLabel)
	c.Cordon("c")
	c.Release("c", &evicted)
	_, reason := c.Place(&evicted)
	if !strings.Contains(reason, "2 pod anti-affinity") || !strings.Contains(reason, "1 cordoned") {
		t.Fatalf("reason = %q", reason)
	}

	// After c is upgraded and uncordoned, the pod fits there again.
	c.Uncordon("c")
	if node, _ := c.Place(&evicted); node != "c" {
		t.Fatalf("got %q, want c", node)
	}
}

func TestAntiAffinityIsSymmetric(t *testing.T) {
	guard := spreadPod("guard", "a", "guard", hostnameLabel)
	guard.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].LabelSelector =
		&metav1.LabelSelector{MatchLabels: map[string]string{"app": "noisy"}}
	c := New([]corev1.Node{testNode("a", "4", "8Gi", nil), testNode("b", "4", "8Gi", nil)}, []corev1.Pod{guard})

	noisy := testPod("noisy", "", "100m", "64Mi")
	noisy.Labels = map[string]string{"app": "noisy"}
	if node, _ := c.Place(&noisy); node != "b" {
		t.Fatalf("existing pod's anti-affinity must keep noisy off node a, got %q", node)
	}
}

func TestAntiAffinityZoneTopology(t *testing.T) {
	zone := "topology.kubernetes.io/zone"
	c := New([]corev1.Node{
		testNode("a1", "4", "8Gi", map[string]string{zone: "a"}),
		testNode("a2", "4", "8Gi", map[string]string{zone: "a"}),
		testNode("b1", "4", "8Gi", map[string]string{zone: "b"}),
	}, []corev1.Pod{spreadPod("db-0", "a1", "db", zone)})

	p := spreadPod("db-1", "", "db", zone)
	if node, _ := c.Place(&p); node != "b1" {
		t.Fatalf("zone a is taken, want b1, got %q", node)
	}
}

func TestTermMatchesNamespaces(t *testing.T) {
	term := corev1.PodAffinityTerm{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}
	other := testPod("web", "", "1m", "1Mi")
	other.Labels = map[string]string{"app": "web"}
	other.Namespace = "other"

	if termMatches(term, "app", &other) {
		t.Error("a term without namespaces only applies to the owner's namespace")
	}
	term.Namespaces = []string{"other"}
	if !termMatches(term, "app", &other) {
		t.Error("explicit namespaces should be honoured")
	}
	term.Namespaces = nil
	term.NamespaceSelector = &metav1.LabelSelector{}
	if !termMatches(term, "app", &other) {
		t.Error("a namespaceSelector is treated as matching every namespace")
	}
	term.LabelSelector = nil
	if termMatches(term, "app", &other) {
		t.Error("a nil label selector matches nothing")
	}
}

func TestSameDomainHostnameFallback(t *testing.T) {
	a, b := testNode("a", "1", "1Gi", nil), testNode("b", "1", "1Gi", nil)
	if !sameDomain(&a, &a, hostnameLabel) || sameDomain(&a, &b, hostnameLabel) {
		t.Fatal("without the hostname label, each node is its own domain")
	}
	if sameDomain(&a, &a, "topology.kubernetes.io/zone") {
		t.Fatal("nodes without a zone label share no zone")
	}
}
