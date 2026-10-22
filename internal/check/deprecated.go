package check

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/kubever"
)

// DeprecatedAPICheck reads the API server metric apiserver_requested_deprecated_apis,
// which records every deprecated API version that a client has actually called
// since the API server started. This catches controllers and CI jobs that would
// break after the upgrade even when nothing is stored in that version.
type DeprecatedAPICheck struct{}

func (DeprecatedAPICheck) Name() string { return "deprecated-apis" }
func (DeprecatedAPICheck) Description() string {
	return "No client is still calling an API version that is removed by the target release"
}

const deprecatedMetric = "apiserver_requested_deprecated_apis"

// deprecatedUse is one series of the deprecated API metric.
type deprecatedUse struct {
	Group, Version, Resource, Subresource, RemovedRelease string
}

func (u deprecatedUse) apiVersion() string {
	if u.Group == "" {
		return u.Version
	}
	return u.Group + "/" + u.Version
}

func (c DeprecatedAPICheck) Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error) {
	if cl.Metrics == nil {
		return []Finding{warn(c.Name(), "apiserver", "metrics are not available; deprecated API usage was not checked")}, nil
	}
	raw, err := cl.Metrics.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch apiserver metrics (needs get on nonResourceURL /metrics): %w", err)
	}
	uses := parseDeprecatedUses(raw)

	var out []Finding
	for _, u := range uses {
		res := u.apiVersion() + " " + u.Resource
		if u.Subresource != "" {
			res += "/" + u.Subresource
		}
		if u.RemovedRelease == "" {
			out = append(out, warn(c.Name(), res, "deprecated API is in use (no removal release announced yet)"))
			continue
		}
		removedIn, err := kubever.Parse(u.RemovedRelease)
		if err != nil {
			out = append(out, warn(c.Name(), res, "deprecated API is in use; "+err.Error()))
			continue
		}
		if removedIn.Compare(opt.Target) <= 0 {
			out = append(out, fail(c.Name(), res, fmt.Sprintf("still requested by clients but removed in %s", removedIn)))
		} else {
			out = append(out, warn(c.Name(), res, fmt.Sprintf("deprecated; removed in %s", removedIn)))
		}
	}
	if len(out) == 0 {
		out = append(out, pass(c.Name(), "apiserver", "no deprecated API requests recorded since the API server started"))
	}
	return out, nil
}

// parseDeprecatedUses extracts the deprecated API series from Prometheus text
// format. Only series with value 1 are in use; 0 means the series was reset.
func parseDeprecatedUses(raw []byte) []deprecatedUse {
	seen := map[deprecatedUse]bool{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, deprecatedMetric+"{") {
			continue
		}
		end := strings.LastIndex(line, "}")
		if end < 0 || strings.TrimSpace(line[end+1:]) != "1" {
			continue
		}
		labels := parseLabels(line[len(deprecatedMetric)+1 : end])
		seen[deprecatedUse{
			Group:          labels["group"],
			Version:        labels["version"],
			Resource:       labels["resource"],
			Subresource:    labels["subresource"],
			RemovedRelease: labels["removed_release"],
		}] = true
	}
	out := make([]deprecatedUse, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].apiVersion()+out[i].Resource < out[j].apiVersion()+out[j].Resource
	})
	return out
}

// parseLabels parses `a="x",b="y"`. Label values in this metric never contain
// quotes or commas, so a full Prometheus parser is not needed.
func parseLabels(s string) map[string]string {
	m := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return m
}
