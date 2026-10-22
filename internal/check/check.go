// Package check implements checks that find what would block or break a
// Kubernetes control plane and node upgrade before it is started.
package check

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/client-go/kubernetes"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/kubever"
)

// Status is the outcome of a single finding.
type Status string

// Finding statuses, from best to worst.
const (
	StatusPass Status = "PASS"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

func (s Status) severity() int {
	switch s {
	case StatusFail:
		return 2
	case StatusWarn:
		return 1
	default:
		return 0
	}
}

// Worse reports whether s is more severe than other.
func (s Status) Worse(other Status) bool { return s.severity() > other.severity() }

// Finding is one observation produced by a check.
type Finding struct {
	Check    string `json:"check"`
	Status   Status `json:"status"`
	Resource string `json:"resource"`
	Message  string `json:"message"`
}

// MetricsFetcher returns the API server's Prometheus metrics in text format.
// It is an interface so checks can be tested without a real API server.
type MetricsFetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// Cluster bundles the clients a check may use.
type Cluster struct {
	Client  kubernetes.Interface
	Metrics MetricsFetcher
}

// Options configure a run.
type Options struct {
	// Target is the Kubernetes version the cluster will be upgraded to.
	Target kubever.Version
	// Namespaces limits namespaced checks. Empty means all namespaces.
	Namespaces []string
	// ExcludeNamespaces are skipped even when Namespaces is empty.
	ExcludeNamespaces []string
	// ServerDryRun makes the drain simulation also call the Eviction API with
	// dryRun=All, which needs create permission on pods/eviction.
	ServerDryRun bool
}

func (o Options) inScope(ns string) bool {
	for _, ex := range o.ExcludeNamespaces {
		if ex == ns {
			return false
		}
	}
	if len(o.Namespaces) == 0 {
		return true
	}
	for _, n := range o.Namespaces {
		if n == ns {
			return true
		}
	}
	return false
}

// Checker is a single named check.
type Checker interface {
	Name() string
	Description() string
	Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error)
}

// Select filters checks by name. Unknown names are rejected so a typo in
// --skip cannot silently leave a check enabled.
func Select(all []Checker, only, skip []string) ([]Checker, error) {
	known := map[string]bool{}
	for _, c := range all {
		known[c.Name()] = true
	}
	for _, n := range append(append([]string{}, only...), skip...) {
		if !known[n] {
			return nil, fmt.Errorf("unknown check %q", n)
		}
	}
	onlySet, skipSet := toSet(only), toSet(skip)
	var out []Checker
	for _, c := range all {
		if (len(onlySet) > 0 && !onlySet[c.Name()]) || skipSet[c.Name()] {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// Report is the aggregated result of a run.
type Report struct {
	Target   string         `json:"target"`
	Findings []Finding      `json:"findings"`
	Errors   []CheckError   `json:"errors,omitempty"`
	Summary  map[Status]int `json:"summary"`
	Overall  Status         `json:"overall"`
}

// CheckError records a check that could not run, e.g. because of missing RBAC.
type CheckError struct {
	Check string `json:"check"`
	Error string `json:"error"`
}

// Run executes the checks and aggregates their findings. A check that errors
// is recorded and makes the run FAIL, because an unverified upgrade is not safe.
func Run(ctx context.Context, cl Cluster, checks []Checker, opt Options) Report {
	r := Report{
		Target:  opt.Target.String(),
		Summary: map[Status]int{StatusPass: 0, StatusWarn: 0, StatusFail: 0},
		Overall: StatusPass,
	}
	for _, c := range checks {
		fs, err := c.Run(ctx, cl, opt)
		if err != nil {
			r.Errors = append(r.Errors, CheckError{Check: c.Name(), Error: err.Error()})
			r.Overall = StatusFail
			continue
		}
		for _, f := range fs {
			r.Summary[f.Status]++
			if f.Status.Worse(r.Overall) {
				r.Overall = f.Status
			}
		}
		r.Findings = append(r.Findings, fs...)
	}
	sort.SliceStable(r.Findings, func(i, j int) bool {
		return r.Findings[i].Status.Worse(r.Findings[j].Status)
	})
	return r
}

func pass(check, resource, msg string) Finding {
	return Finding{Check: check, Status: StatusPass, Resource: resource, Message: msg}
}

func warn(check, resource, msg string) Finding {
	return Finding{Check: check, Status: StatusWarn, Resource: resource, Message: msg}
}

func fail(check, resource, msg string) Finding {
	return Finding{Check: check, Status: StatusFail, Resource: resource, Message: msg}
}
