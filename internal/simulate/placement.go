// Package simulate models where pods would be scheduled, so that a rolling
// node drain can be dry-run in memory without touching the cluster.
//
// It applies the scheduler's hard constraints that matter during a drain:
// cordoned and NotReady nodes, nodeSelector and required node affinity,
// NoSchedule/NoExecute taints, required pod anti-affinity, and CPU, memory
// and pod-count requests. Required pod affinity, topology spread and volume
// zone constraints are not modelled.
package simulate

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcehelper "k8s.io/component-helpers/resource"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"
)

// Usage is the amount of schedulable resources requested on a node.
type Usage struct {
	MilliCPU int64
	Memory   int64
	Pods     int64
}

func (u Usage) add(o Usage) Usage {
	return Usage{u.MilliCPU + o.MilliCPU, u.Memory + o.Memory, u.Pods + o.Pods}
}

func (u Usage) sub(o Usage) Usage {
	return Usage{u.MilliCPU - o.MilliCPU, u.Memory - o.Memory, u.Pods - o.Pods}
}

// Requests returns the scheduling requests of a pod, including init
// containers, sidecars and pod overhead, as computed by the scheduler.
func Requests(p *corev1.Pod) Usage {
	r := resourcehelper.PodRequests(p, resourcehelper.PodResourcesOptions{})
	return Usage{
		MilliCPU: r.Cpu().MilliValue(),
		Memory:   r.Memory().Value(),
		Pods:     1,
	}
}

type nodeState struct {
	node        *corev1.Node
	allocatable Usage
	used        Usage
	cordoned    bool
	pods        []*corev1.Pod
}

func (n *nodeState) free() Usage { return n.allocatable.sub(n.used) }

// Cluster is an in-memory model of nodes and the pods bound to them.
type Cluster struct {
	nodes  []*nodeState
	byName map[string]*nodeState
}

// New builds a model from live nodes and pods. Terminated pods do not hold
// resources and are ignored.
func New(nodes []corev1.Node, pods []corev1.Pod) *Cluster {
	c := &Cluster{byName: map[string]*nodeState{}}
	for i := range nodes {
		n := &nodes[i]
		a := n.Status.Allocatable
		st := &nodeState{
			node: n,
			allocatable: Usage{
				MilliCPU: a.Cpu().MilliValue(),
				Memory:   a.Memory().Value(),
				Pods:     a.Pods().Value(),
			},
			cordoned: n.Spec.Unschedulable,
		}
		c.nodes = append(c.nodes, st)
		c.byName[n.Name] = st
	}
	sort.Slice(c.nodes, func(i, j int) bool { return c.nodes[i].node.Name < c.nodes[j].node.Name })
	for i := range pods {
		p := &pods[i]
		if IsTerminated(p) {
			continue
		}
		if st, ok := c.byName[p.Spec.NodeName]; ok {
			st.used = st.used.add(Requests(p))
			st.pods = append(st.pods, p)
		}
	}
	return c
}

// IsTerminated reports whether a pod no longer consumes node resources.
func IsTerminated(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

// Cordon marks a node unschedulable, as `kubectl drain` does first.
func (c *Cluster) Cordon(name string) {
	if st, ok := c.byName[name]; ok {
		st.cordoned = true
	}
}

// Uncordon makes a node schedulable again, as after it has been upgraded.
func (c *Cluster) Uncordon(name string) {
	if st, ok := c.byName[name]; ok {
		st.cordoned = st.node.Spec.Unschedulable
	}
}

// Release frees the resources a pod held on a node.
func (c *Cluster) Release(nodeName string, p *corev1.Pod) {
	st, ok := c.byName[nodeName]
	if !ok {
		return
	}
	for i, q := range st.pods {
		if q.Namespace == p.Namespace && q.Name == p.Name {
			st.pods = append(st.pods[:i], st.pods[i+1:]...)
			st.used = st.used.sub(Requests(p))
			return
		}
	}
}

// Place finds a node for the pod, reserves its resources and returns the
// node name. When no node fits it returns a scheduler-style explanation such
// as "0/3 nodes are available: 1 cordoned, 2 insufficient memory".
func (c *Cluster) Place(p *corev1.Pod) (string, string) {
	req := Requests(p)
	affinity := nodeaffinity.GetRequiredNodeAffinity(p)
	reasons := map[string]int{}

	var best *nodeState
	for _, st := range c.nodes {
		reason := c.unfit(st, p, req, affinity)
		if reason != "" {
			reasons[reason]++
			continue
		}
		// Prefer the node with the most free memory, similar to the
		// scheduler's default LeastAllocated scoring.
		if best == nil || st.free().Memory > best.free().Memory {
			best = st
		}
	}
	if best == nil {
		return "", explain(len(c.nodes), reasons)
	}
	best.used = best.used.add(req)
	best.pods = append(best.pods, p)
	return best.node.Name, ""
}

func (c *Cluster) unfit(st *nodeState, p *corev1.Pod, req Usage, affinity nodeaffinity.RequiredNodeAffinity) string {
	if st.cordoned {
		return "cordoned"
	}
	if !isReady(st.node) {
		return "not ready"
	}
	if ok, _ := affinity.Match(st.node); !ok {
		return "node selector/affinity mismatch"
	}
	if t, found := corev1helpers.FindMatchingUntoleratedTaint(st.node.Spec.Taints, p.Spec.Tolerations, hardTaint); found {
		return "untolerated taint " + t.Key
	}
	if c.violatesAntiAffinity(st, p) {
		return "pod anti-affinity"
	}
	free := st.free()
	switch {
	case req.Pods > free.Pods:
		return "too many pods"
	case req.MilliCPU > free.MilliCPU:
		return "insufficient cpu"
	case req.Memory > free.Memory:
		return "insufficient memory"
	}
	return ""
}

func hardTaint(t *corev1.Taint) bool {
	return t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute
}

func isReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func explain(total int, reasons map[string]int) string {
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", reasons[k], k))
	}
	return fmt.Sprintf("0/%d nodes are available: %s", total, strings.Join(parts, ", "))
}
