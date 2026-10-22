package check

import (
	"testing"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pdb(ns, name string, selector map[string]string, allowed, healthy, desired, expected int32) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: selector}},
		Status: policyv1.PodDisruptionBudgetStatus{
			DisruptionsAllowed: allowed,
			CurrentHealthy:     healthy,
			DesiredHealthy:     desired,
			ExpectedPods:       expected,
		},
	}
}

func TestPDBCheck(t *testing.T) {
	cs := newFake("v1.34.1",
		pdb("app", "ok", map[string]string{"app": "api"}, 1, 3, 2, 3),
		pdb("app", "strict", map[string]string{"app": "db"}, 0, 2, 2, 2),
		pdb("app", "degraded", map[string]string{"app": "cache"}, 0, 1, 2, 3),
		pdb("app", "orphan", map[string]string{"app": "gone"}, 0, 0, 0, 0),
	)
	fs := runCheck(t, PDBCheck{}, Cluster{Client: cs}, testOptions("v1.35"))
	expectStatus(t, fs, "pdb/app/ok", StatusPass)
	expectStatus(t, fs, "pdb/app/strict", StatusFail)
	expectStatus(t, fs, "pdb/app/degraded", StatusFail)
	expectStatus(t, fs, "pdb/app/orphan", StatusWarn)
}
