package check

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PDBCheck finds PodDisruptionBudgets that currently allow zero disruptions.
// `kubectl drain` waits forever on such a budget, which is the most common
// reason a node upgrade stalls halfway.
type PDBCheck struct{}

func (PDBCheck) Name() string { return "pdb" }
func (PDBCheck) Description() string {
	return "PodDisruptionBudgets allow at least one disruption so nodes can be drained"
}

func (c PDBCheck) Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error) {
	pdbs, err := cl.Client.PolicyV1().PodDisruptionBudgets(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list poddisruptionbudgets: %w", err)
	}
	var out []Finding
	for _, p := range pdbs.Items {
		if !opt.inScope(p.Namespace) {
			continue
		}
		res := "pdb/" + p.Namespace + "/" + p.Name
		st := p.Status
		switch {
		case st.ExpectedPods == 0:
			out = append(out, warn(c.Name(), res, "selects no pods; check the label selector"))
		case st.DisruptionsAllowed == 0 && st.CurrentHealthy < st.DesiredHealthy:
			out = append(out, fail(c.Name(), res, fmt.Sprintf("already below budget (%d/%d healthy); drains will block until the workload recovers",
				st.CurrentHealthy, st.DesiredHealthy)))
		case st.DisruptionsAllowed == 0:
			out = append(out, fail(c.Name(), res, fmt.Sprintf("allows 0 disruptions (%d healthy, %d required); drains will block",
				st.CurrentHealthy, st.DesiredHealthy)))
		default:
			out = append(out, pass(c.Name(), res, fmt.Sprintf("allows %d disruption(s)", st.DisruptionsAllowed)))
		}
	}
	return out, nil
}
