package check

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/kubever"
)

func testOptions(target string) Options {
	return Options{Target: kubever.MustParse(target)}
}

// newFake returns a fake clientset whose API server reports serverVersion.
func newFake(serverVersion string, objs ...runtime.Object) *fake.Clientset {
	cs := fake.NewClientset(objs...)
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: serverVersion}
	return cs
}

func runCheck(t *testing.T, c Checker, cl Cluster, opt Options) []Finding {
	t.Helper()
	fs, err := c.Run(context.Background(), cl, opt)
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", c.Name(), err)
	}
	return fs
}

func findingsFor(fs []Finding, resource string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Resource == resource {
			out = append(out, f)
		}
	}
	return out
}

func expectStatus(t *testing.T, fs []Finding, resource string, want Status) Finding {
	t.Helper()
	got := findingsFor(fs, resource)
	if len(got) == 0 {
		t.Fatalf("no finding for %s in %+v", resource, fs)
	}
	for _, f := range got {
		if f.Status == want {
			return f
		}
	}
	t.Fatalf("%s: no %s finding in %+v", resource, want, got)
	return Finding{}
}

func TestInScope(t *testing.T) {
	o := Options{Namespaces: []string{"app"}, ExcludeNamespaces: []string{"app-old"}}
	for ns, want := range map[string]bool{"app": true, "other": false, "app-old": false} {
		if got := o.inScope(ns); got != want {
			t.Errorf("inScope(%q) = %v, want %v", ns, got, want)
		}
	}
}

func TestSelect(t *testing.T) {
	got, err := Select(All(), nil, []string{"helm-releases"})
	if err != nil || len(got) != len(All())-1 {
		t.Fatalf("skip: %v %v", got, err)
	}
	only, err := Select(All(), []string{"drain-dry-run"}, nil)
	if err != nil || len(only) != 1 || only[0].Name() != "drain-dry-run" {
		t.Fatalf("only: %v %v", only, err)
	}
	if _, err := Select(All(), nil, []string{"typo"}); err == nil {
		t.Fatal("unknown check names must be rejected")
	}
}

type stubCheck struct {
	name string
	fs   []Finding
	err  error
}

func (s stubCheck) Name() string        { return s.name }
func (s stubCheck) Description() string { return "stub" }
func (s stubCheck) Run(context.Context, Cluster, Options) ([]Finding, error) {
	return s.fs, s.err
}

func TestRun(t *testing.T) {
	checks := []Checker{
		stubCheck{name: "a", fs: []Finding{pass("a", "x", ""), warn("a", "y", "")}},
		stubCheck{name: "broken", err: errors.New("forbidden")},
	}
	r := Run(context.Background(), Cluster{}, checks, testOptions("v1.35"))
	if r.Overall != StatusFail || r.Target != "v1.35" || len(r.Errors) != 1 {
		t.Fatalf("got %+v", r)
	}
	if r.Findings[0].Status != StatusWarn {
		t.Fatal("findings should be sorted worst first")
	}
}
