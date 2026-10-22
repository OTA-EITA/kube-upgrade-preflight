package check

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestAvailabilityCheck(t *testing.T) {
	cs := newFake("v1.34.1",
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "api"}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](3)}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "admin"}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "paused"}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](0)}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "db", Name: "postgres"}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To[int32](1)}},
	)
	fs := runCheck(t, AvailabilityCheck{}, Cluster{Client: cs}, testOptions("v1.35"))
	if len(fs) != 2 {
		t.Fatalf("want 2 single-replica findings, got %+v", fs)
	}
	expectStatus(t, fs, "deployment/app/admin", StatusWarn)
	expectStatus(t, fs, "statefulset/db/postgres", StatusWarn)
}

func TestAvailabilityCheckClean(t *testing.T) {
	fs := runCheck(t, AvailabilityCheck{}, Cluster{Client: newFake("v1.34.1")}, testOptions("v1.35"))
	expectStatus(t, fs, "cluster", StatusPass)
}
