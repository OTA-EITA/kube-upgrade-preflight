package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/check"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		overall check.Status
		failOn  string
		want    int
	}{
		{check.StatusPass, "fail", exitOK},
		{check.StatusWarn, "fail", exitOK},
		{check.StatusWarn, "warn", exitFail},
		{check.StatusFail, "fail", exitFail},
	}
	for _, c := range cases {
		if got := exitCode(c.overall, c.failOn); got != c.want {
			t.Errorf("exitCode(%s, %s) = %d, want %d", c.overall, c.failOn, got, c.want)
		}
	}
}

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags([]string{
		"--target", "v1.35", "--server-dry-run",
		"--namespace", "app,db", "--namespace", "web", "--skip", "helm-releases",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.target != "v1.35" || !cfg.serverDryRun || cfg.namespaces.String() != "app,db,web" || cfg.skip.String() != "helm-releases" {
		t.Errorf("unexpected config %+v", cfg)
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--target", "latest"},
		{"--target", "v1.35", "--fail-on", "maybe"},
		{"--target", "v1.35", "--output", "yaml"},
		{"--target", "v1.35", "--skip", "typo"},
	} {
		var stderr bytes.Buffer
		if code := run(args, io.Discard, &stderr); code != exitUsage {
			t.Errorf("run(%v) = %d, want %d (stderr: %s)", args, code, exitUsage, stderr.String())
		}
	}
}

func TestRunList(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"--list"}, &out, io.Discard); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, c := range check.All() {
		if !strings.Contains(out.String(), c.Name()) {
			t.Errorf("--list is missing %s", c.Name())
		}
	}
}
