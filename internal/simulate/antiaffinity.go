package simulate

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// hostnameLabel is the topology key used by "one replica per node" rules.
const hostnameLabel = "kubernetes.io/hostname"

// violatesAntiAffinity reports whether placing p on st breaks a required pod
// anti-affinity rule, either p's own rule or that of a pod already running in
// the same topology domain (the scheduler enforces both directions).
func (c *Cluster) violatesAntiAffinity(st *nodeState, p *corev1.Pod) bool {
	for _, term := range requiredAntiAffinity(p) {
		for _, other := range c.nodes {
			if !sameDomain(st.node, other.node, term.TopologyKey) {
				continue
			}
			for _, q := range other.pods {
				if termMatches(term, p.Namespace, q) {
					return true
				}
			}
		}
	}
	for _, other := range c.nodes {
		for _, q := range other.pods {
			for _, term := range requiredAntiAffinity(q) {
				if sameDomain(st.node, other.node, term.TopologyKey) && termMatches(term, q.Namespace, p) {
					return true
				}
			}
		}
	}
	return false
}

func requiredAntiAffinity(p *corev1.Pod) []corev1.PodAffinityTerm {
	if p.Spec.Affinity == nil || p.Spec.Affinity.PodAntiAffinity == nil {
		return nil
	}
	return p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
}

// sameDomain reports whether two nodes share the value of a topology label.
// Nodes always form their own hostname domain, even if the label is missing.
func sameDomain(a, b *corev1.Node, key string) bool {
	va, okA := a.Labels[key]
	vb, okB := b.Labels[key]
	if !okA || !okB {
		return key == hostnameLabel && a.Name == b.Name
	}
	return va == vb
}

// termMatches reports whether pod q is selected by a term declared by a pod in
// ownerNS. A namespaceSelector cannot be evaluated without Namespace objects,
// so it is treated as matching every namespace, which errs on the safe side.
func termMatches(term corev1.PodAffinityTerm, ownerNS string, q *corev1.Pod) bool {
	if term.LabelSelector == nil {
		return false
	}
	if term.NamespaceSelector == nil {
		namespaces := term.Namespaces
		if len(namespaces) == 0 {
			namespaces = []string{ownerNS}
		}
		found := false
		for _, ns := range namespaces {
			if ns == q.Namespace {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	sel, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
	if err != nil {
		return false
	}
	return sel.Matches(labels.Set(q.Labels))
}
