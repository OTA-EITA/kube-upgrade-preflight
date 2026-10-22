package check

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AvailabilityCheck warns about Deployments and StatefulSets with a single
// replica: every node drain during the upgrade takes them down until the
// replacement pod is scheduled and ready.
type AvailabilityCheck struct{}

func (AvailabilityCheck) Name() string { return "single-replica" }
func (AvailabilityCheck) Description() string {
	return "Deployments and StatefulSets run more than one replica so drains do not cause downtime"
}

func (c AvailabilityCheck) Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error) {
	var out []Finding
	deps, err := cl.Client.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	for _, d := range deps.Items {
		if opt.inScope(d.Namespace) && d.Spec.Replicas != nil && *d.Spec.Replicas == 1 {
			out = append(out, warn(c.Name(), "deployment/"+d.Namespace+"/"+d.Name, "1 replica; unavailable while its node is drained"))
		}
	}
	sts, err := cl.Client.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list statefulsets: %w", err)
	}
	for _, s := range sts.Items {
		if opt.inScope(s.Namespace) && s.Spec.Replicas != nil && *s.Spec.Replicas == 1 {
			out = append(out, warn(c.Name(), "statefulset/"+s.Namespace+"/"+s.Name, "1 replica; unavailable while its node is drained"))
		}
	}
	if len(out) == 0 {
		out = append(out, pass(c.Name(), "cluster", "no single-replica Deployments or StatefulSets"))
	}
	return out, nil
}
