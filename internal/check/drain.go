package check

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/simulate"
)

// DrainSimulation dry-runs a rolling upgrade: every node is cordoned and
// drained in turn, its pods are re-placed on the remaining nodes with the
// in-memory scheduler model, and the node is uncordoned again before moving
// on, exactly like a node-by-node upgrade. Nothing in the cluster is changed.
//
// With Options.ServerDryRun the Eviction API is additionally called with
// dryRun=All for every pod, so the API server itself confirms whether a
// PodDisruptionBudget would reject the eviction.
type DrainSimulation struct{}

func (DrainSimulation) Name() string { return "drain-dry-run" }
func (DrainSimulation) Description() string {
	return "Simulates draining every node in turn: PDBs, unmanaged pods, emptyDir data and room to reschedule"
}

const (
	mirrorPodAnnotation = "kubernetes.io/config.mirror"
	controlPlaneLabel   = "node-role.kubernetes.io/control-plane"
)

func (c DrainSimulation) Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error) {
	nodeList, err := cl.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	podList, err := cl.Client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	pdbList, err := cl.Client.PolicyV1().PodDisruptionBudgets(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list poddisruptionbudgets: %w", err)
	}

	model := simulate.New(nodeList.Items, podList.Items)
	podsByNode := map[string][]*corev1.Pod{}
	for i := range podList.Items {
		p := &podList.Items[i]
		if !simulate.IsTerminated(p) && p.Spec.NodeName != "" {
			podsByNode[p.Spec.NodeName] = append(podsByNode[p.Spec.NodeName], p)
		}
	}

	var out []Finding
	for _, n := range drainOrder(nodeList.Items) {
		out = append(out, c.drainNode(ctx, cl, opt, model, n.Name, podsByNode, pdbList.Items)...)
	}
	return out, nil
}

// drainOrder upgrades control plane nodes first, then workers, each by name.
func drainOrder(nodes []corev1.Node) []corev1.Node {
	out := append([]corev1.Node{}, nodes...)
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := isControlPlane(out[i]), isControlPlane(out[j])
		if ci != cj {
			return ci
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func isControlPlane(n corev1.Node) bool {
	_, ok := n.Labels[controlPlaneLabel]
	return ok
}

// drainNode simulates draining one node. podsByNode is the simulated pod
// placement and is updated, so pods moved here by an earlier drain are
// evicted again when this node's turn comes.
func (c DrainSimulation) drainNode(ctx context.Context, cl Cluster, opt Options, model *simulate.Cluster,
	node string, podsByNode map[string][]*corev1.Pod, pdbs []policyv1.PodDisruptionBudget) []Finding {

	res := "node/" + node
	pods := podsByNode[node]
	var staying []*corev1.Pod
	model.Cordon(node)
	defer model.Uncordon(node)

	var out []Finding
	report := func(f Finding) { out = append(out, f) }

	var evicted []*corev1.Pod
	pdbUse := map[string]int{}
	for _, p := range pods {
		if isMirrorPod(p) || isDaemonSetPod(p) {
			staying = append(staying, p) // drain leaves these on the node
			continue
		}
		model.Release(node, p)
		visible := opt.inScope(p.Namespace)
		name := p.Namespace + "/" + p.Name

		if metav1.GetControllerOf(p) == nil {
			if visible {
				report(fail(c.Name(), res, fmt.Sprintf("pod %s has no controller; drain needs --force and the pod is gone for good", name)))
			}
			continue
		}
		evicted = append(evicted, p)
		if visible && usesEmptyDir(p) {
			report(warn(c.Name(), res, fmt.Sprintf("pod %s uses emptyDir; its data is lost (drain needs --delete-emptydir-data)", name)))
		}
		for _, pdb := range matchingPDBs(p, pdbs) {
			pdbUse[pdb.Namespace+"/"+pdb.Name]++
		}
		if visible && opt.ServerDryRun {
			if f, ok := c.serverDryRunEvict(ctx, cl, res, p); ok {
				report(f)
			}
		}
	}

	for _, pdb := range pdbs {
		key := pdb.Namespace + "/" + pdb.Name
		used := pdbUse[key]
		if used == 0 || !opt.inScope(pdb.Namespace) {
			continue
		}
		allowed := int(pdb.Status.DisruptionsAllowed)
		switch {
		case allowed == 0:
			report(fail(c.Name(), res, fmt.Sprintf("PDB %s allows 0 disruptions; evicting %d pod(s) here blocks the drain", key, used)))
		case used > allowed:
			report(warn(c.Name(), res, fmt.Sprintf("PDB %s allows %d disruption(s) but %d pods are on this node; the drain waits for replacements to become ready", key, allowed, used)))
		}
	}

	// Re-place the largest pods first, like a bin-packing scheduler would.
	sort.SliceStable(evicted, func(i, j int) bool {
		return simulate.Requests(evicted[i]).Memory > simulate.Requests(evicted[j]).Memory
	})
	podsByNode[node] = staying
	var stuck []string
	for _, p := range evicted {
		target, reason := model.Place(p)
		if reason == "" {
			podsByNode[target] = append(podsByNode[target], p)
			continue
		}
		if opt.inScope(p.Namespace) {
			stuck = append(stuck, fmt.Sprintf("%s/%s (%s)", p.Namespace, p.Name, reason))
		}
	}
	if len(stuck) > 0 {
		report(fail(c.Name(), res, "cannot reschedule while this node is drained: "+strings.Join(stuck, "; ")))
	}

	if len(out) == 0 {
		report(pass(c.Name(), res, fmt.Sprintf("%d pod(s) can be evicted and rescheduled", len(evicted))))
	}
	return out
}

// serverDryRunEvict asks the API server to evaluate an eviction without
// performing it. A 429 means a PodDisruptionBudget would reject it.
func (c DrainSimulation) serverDryRunEvict(ctx context.Context, cl Cluster, res string, p *corev1.Pod) (Finding, bool) {
	ev := &policyv1.Eviction{
		ObjectMeta:    metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
		DeleteOptions: &metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}},
	}
	err := cl.Client.CoreV1().Pods(p.Namespace).EvictV1(ctx, ev)
	name := p.Namespace + "/" + p.Name
	switch {
	case err == nil:
		return Finding{}, false
	case apierrors.IsTooManyRequests(err):
		return fail(c.Name(), res, fmt.Sprintf("API server rejected dry-run eviction of %s: %v", name, err)), true
	default:
		return warn(c.Name(), res, fmt.Sprintf("dry-run eviction of %s failed: %v", name, err)), true
	}
}

func isMirrorPod(p *corev1.Pod) bool {
	_, ok := p.Annotations[mirrorPodAnnotation]
	return ok
}

func isDaemonSetPod(p *corev1.Pod) bool {
	ref := metav1.GetControllerOf(p)
	return ref != nil && ref.Kind == "DaemonSet"
}

func usesEmptyDir(p *corev1.Pod) bool {
	for _, v := range p.Spec.Volumes {
		if v.EmptyDir != nil {
			return true
		}
	}
	return false
}

// matchingPDBs mirrors the eviction API: a nil selector matches nothing and
// an empty selector matches every pod in the namespace.
func matchingPDBs(p *corev1.Pod, pdbs []policyv1.PodDisruptionBudget) []policyv1.PodDisruptionBudget {
	var out []policyv1.PodDisruptionBudget
	for _, pdb := range pdbs {
		if pdb.Namespace != p.Namespace || pdb.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil || !sel.Matches(labels.Set(p.Labels)) {
			continue
		}
		out = append(out, pdb)
	}
	return out
}
