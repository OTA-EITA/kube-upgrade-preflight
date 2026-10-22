package check

import (
	"context"
	"errors"
	"testing"
)

type staticMetrics struct {
	body string
	err  error
}

func (m staticMetrics) Fetch(context.Context) ([]byte, error) { return []byte(m.body), m.err }

const sampleMetrics = `# HELP apiserver_requested_deprecated_apis [STABLE] Gauge of deprecated APIs that have been requested, broken out by API group, version, resource, subresource, and removed_release.
# TYPE apiserver_requested_deprecated_apis gauge
apiserver_requested_deprecated_apis{group="flowcontrol.apiserver.k8s.io",removed_release="1.32",resource="flowschemas",subresource="",version="v1beta3"} 1
apiserver_requested_deprecated_apis{group="example.com",removed_release="1.40",resource="widgets",subresource="status",version="v1alpha1"} 1
apiserver_requested_deprecated_apis{group="",removed_release="",resource="componentstatuses",subresource="",version="v1"} 1
apiserver_requested_deprecated_apis{group="batch",removed_release="1.25",resource="cronjobs",subresource="",version="v1beta1"} 0
apiserver_request_total{code="200",resource="pods",verb="LIST",version="v1"} 42
`

func TestDeprecatedAPICheck(t *testing.T) {
	fs := runCheck(t, DeprecatedAPICheck{}, Cluster{Metrics: staticMetrics{body: sampleMetrics}}, testOptions("v1.32"))
	expectStatus(t, fs, "flowcontrol.apiserver.k8s.io/v1beta3 flowschemas", StatusFail)
	expectStatus(t, fs, "example.com/v1alpha1 widgets/status", StatusWarn)
	expectStatus(t, fs, "v1 componentstatuses", StatusWarn)
	if len(findingsFor(fs, "batch/v1beta1 cronjobs")) != 0 {
		t.Error("series with value 0 are not in use and must be ignored")
	}
}

func TestDeprecatedAPICheckTargetBeforeRemoval(t *testing.T) {
	fs := runCheck(t, DeprecatedAPICheck{}, Cluster{Metrics: staticMetrics{body: sampleMetrics}}, testOptions("v1.31"))
	expectStatus(t, fs, "flowcontrol.apiserver.k8s.io/v1beta3 flowschemas", StatusWarn)
}

func TestDeprecatedAPICheckClean(t *testing.T) {
	fs := runCheck(t, DeprecatedAPICheck{}, Cluster{Metrics: staticMetrics{body: "apiserver_request_total 1\n"}}, testOptions("v1.35"))
	expectStatus(t, fs, "apiserver", StatusPass)
}

func TestDeprecatedAPICheckWithoutMetrics(t *testing.T) {
	fs := runCheck(t, DeprecatedAPICheck{}, Cluster{}, testOptions("v1.35"))
	expectStatus(t, fs, "apiserver", StatusWarn)

	_, err := DeprecatedAPICheck{}.Run(context.Background(), Cluster{Metrics: staticMetrics{err: errors.New("forbidden")}}, testOptions("v1.35"))
	if err == nil {
		t.Fatal("a metrics error should make the check error")
	}
}
