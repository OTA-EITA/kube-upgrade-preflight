// Package report renders a check.Report as a table, JSON or Markdown.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/check"
)

// Format is an output format name accepted by Write.
type Format string

// Supported output formats.
const (
	Table    Format = "table"
	JSON     Format = "json"
	Markdown Format = "markdown"
)

// Formats lists the accepted values for --output.
var Formats = []Format{Table, JSON, Markdown}

// ParseFormat validates a user supplied format name.
func ParseFormat(s string) (Format, error) {
	for _, f := range Formats {
		if string(f) == s {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown output format %q (want one of %v)", s, Formats)
}

// Options control what is rendered.
type Options struct {
	// Quiet hides PASS findings so only problems are shown.
	Quiet bool
}

// Write renders r to w in the given format.
func Write(w io.Writer, r check.Report, f Format, opt Options) error {
	if opt.Quiet {
		r.Findings = withoutPass(r.Findings)
	}
	switch f {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case Markdown:
		return writeMarkdown(w, r)
	default:
		return writeTable(w, r)
	}
}

func withoutPass(fs []check.Finding) []check.Finding {
	out := make([]check.Finding, 0, len(fs))
	for _, f := range fs {
		if f.Status != check.StatusPass {
			out = append(out, f)
		}
	}
	return out
}

func writeTable(w io.Writer, r check.Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCHECK\tRESOURCE\tMESSAGE")
	for _, f := range r.Findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Status, f.Check, f.Resource, f.Message)
	}
	for _, e := range r.Errors {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", check.StatusFail, e.Check, "-", "check could not run: "+e.Error)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\ntarget=%s %s\n", r.Target, summaryLine(r))
	return err
}

func writeMarkdown(w io.Writer, r check.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "## kube-upgrade-preflight (target %s): %s\n\n%s\n\n", r.Target, r.Overall, summaryLine(r))
	b.WriteString("| Status | Check | Resource | Message |\n|---|---|---|---|\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "| %s %s | %s | `%s` | %s |\n", emoji(f.Status), f.Status, f.Check, f.Resource, escapePipe(f.Message))
	}
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "| %s %s | %s | - | check could not run: %s |\n", emoji(check.StatusFail), check.StatusFail, e.Check, escapePipe(e.Error))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func summaryLine(r check.Report) string {
	return fmt.Sprintf("overall=%s pass=%d warn=%d fail=%d errors=%d",
		r.Overall, r.Summary[check.StatusPass], r.Summary[check.StatusWarn], r.Summary[check.StatusFail], len(r.Errors))
}

func emoji(s check.Status) string {
	switch s {
	case check.StatusFail:
		return "❌"
	case check.StatusWarn:
		return "⚠️"
	default:
		return "✅"
	}
}

func escapePipe(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
