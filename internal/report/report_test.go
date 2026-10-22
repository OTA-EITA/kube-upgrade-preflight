package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/check"
)

func sampleReport() check.Report {
	return check.Report{
		Target: "v1.35",
		Findings: []check.Finding{
			{Check: "pdb", Status: check.StatusFail, Resource: "pdb/app/api", Message: "allows 0 disruptions (2 healthy, 2 required); drains will block"},
			{Check: "version-skew", Status: check.StatusPass, Resource: "node/a", Message: "kubelet v1.34 | supported"},
		},
		Errors:  []check.CheckError{{Check: "helm-releases", Error: "forbidden"}},
		Summary: map[check.Status]int{check.StatusPass: 1, check.StatusWarn: 0, check.StatusFail: 1},
		Overall: check.StatusFail,
	}
}

func TestParseFormat(t *testing.T) {
	for _, f := range Formats {
		if got, err := ParseFormat(string(f)); err != nil || got != f {
			t.Errorf("ParseFormat(%q) = %q, %v", f, got, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("unknown format should be rejected")
	}
}

func TestWriteTable(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, sampleReport(), Table, Options{}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"STATUS", "pdb/app/api", "check could not run: forbidden", "target=v1.35 overall=FAIL pass=1 warn=0 fail=1 errors=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteQuietHidesPass(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, sampleReport(), Table, Options{Quiet: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "node/a") {
		t.Errorf("quiet output should not include PASS findings:\n%s", b.String())
	}
}

func TestWriteJSON(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, sampleReport(), JSON, Options{}); err != nil {
		t.Fatal(err)
	}
	var got check.Report
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, b.String())
	}
	if got.Overall != check.StatusFail || got.Target != "v1.35" || len(got.Findings) != 2 || got.Summary[check.StatusFail] != 1 {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestWriteMarkdownEscapesPipes(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, sampleReport(), Markdown, Options{}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `v1.34 \| supported`) {
		t.Errorf("pipes in messages must be escaped:\n%s", out)
	}
	if !strings.HasPrefix(out, "## kube-upgrade-preflight (target v1.35): FAIL") {
		t.Errorf("unexpected heading:\n%s", out)
	}
}
