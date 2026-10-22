package check

import "testing"

func TestLookupRemoval(t *testing.T) {
	cases := []struct {
		apiVersion, kind, removedIn, replacement string
	}{
		{"extensions/v1beta1", "Ingress", "v1.22", "networking.k8s.io/v1"},
		{"policy/v1beta1", "PodDisruptionBudget", "v1.25", "policy/v1"},
		{"batch/v1beta1", "CronJob", "v1.25", "batch/v1"},
		{"autoscaling/v2beta2", "HorizontalPodAutoscaler", "v1.26", "autoscaling/v2"},
		{"flowcontrol.apiserver.k8s.io/v1beta3", "FlowSchema", "v1.32", "flowcontrol.apiserver.k8s.io/v1"},
	}
	for _, c := range cases {
		r, ok := LookupRemoval(c.apiVersion, c.kind)
		if !ok {
			t.Errorf("%s %s should be in the removal table", c.apiVersion, c.kind)
			continue
		}
		if r.RemovedIn.String() != c.removedIn || r.Replacement != c.replacement {
			t.Errorf("%s %s = removed %s -> %s, want %s -> %s", c.apiVersion, c.kind, r.RemovedIn, r.Replacement, c.removedIn, c.replacement)
		}
	}
	for _, served := range [][2]string{{"apps/v1", "Deployment"}, {"networking.k8s.io/v1", "Ingress"}, {"policy/v1", "PodDisruptionBudget"}} {
		if _, ok := LookupRemoval(served[0], served[1]); ok {
			t.Errorf("%s %s is still served and must not be reported", served[0], served[1])
		}
	}
}
