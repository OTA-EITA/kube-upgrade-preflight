// Command kube-upgrade-preflight checks, before a Kubernetes upgrade starts,
// everything that would block or break it: the version skew policy, removed
// APIs still in use, Helm releases with removed APIs, PodDisruptionBudgets that
// block drains, and a dry-run of draining every node in turn.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/check"
	"github.com/OTA-EITA/kube-upgrade-preflight/internal/kubever"
	"github.com/OTA-EITA/kube-upgrade-preflight/internal/report"
)

const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type config struct {
	target       string
	kubeconfig   string
	kubeContext  string
	output       string
	failOn       string
	quiet        bool
	serverDryRun bool
	timeout      time.Duration
	namespaces   csvFlag
	excludeNS    csvFlag
	only         csvFlag
	skip         csvFlag
	list         bool
	showVersion  bool
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("kube-upgrade-preflight", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.target, "target", "", "Kubernetes version to upgrade to, e.g. v1.35 (required)")
	fs.StringVar(&cfg.kubeconfig, "kubeconfig", "", "path to kubeconfig (defaults to $KUBECONFIG or ~/.kube/config)")
	fs.StringVar(&cfg.kubeContext, "context", "", "kubeconfig context to use")
	fs.StringVar(&cfg.output, "output", "table", "output format: table, json or markdown")
	fs.StringVar(&cfg.failOn, "fail-on", "fail", "exit non-zero on: fail or warn")
	fs.BoolVar(&cfg.quiet, "quiet", false, "hide PASS findings")
	fs.BoolVar(&cfg.serverDryRun, "server-dry-run", false, "also call the Eviction API with dryRun=All (needs create on pods/eviction)")
	fs.DurationVar(&cfg.timeout, "timeout", 2*time.Minute, "overall timeout for all API calls")
	fs.Var(&cfg.namespaces, "namespace", "comma separated namespaces to report on (default: all)")
	fs.Var(&cfg.excludeNS, "exclude-namespace", "comma separated namespaces to leave out of the report")
	fs.Var(&cfg.only, "only", "comma separated checks to run")
	fs.Var(&cfg.skip, "skip", "comma separated checks to skip")
	fs.BoolVar(&cfg.list, "list", false, "list available checks and exit")
	fs.BoolVar(&cfg.showVersion, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.failOn != "fail" && cfg.failOn != "warn" {
		return cfg, fmt.Errorf("--fail-on must be fail or warn, got %q", cfg.failOn)
	}
	return cfg, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	if cfg.showVersion {
		fmt.Fprintln(stdout, "kube-upgrade-preflight", version)
		return exitOK
	}
	if cfg.list {
		if err := listChecks(stdout); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return exitUsage
		}
		return exitOK
	}
	if cfg.target == "" {
		fmt.Fprintln(stderr, "error: --target is required (e.g. --target v1.35)")
		return exitUsage
	}
	target, err := kubever.Parse(cfg.target)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	format, err := report.ParseFormat(cfg.output)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	checks, err := check.Select(check.All(), cfg.only.vals, cfg.skip.vals)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	cs, err := newClient(cfg.kubeconfig, cfg.kubeContext)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}

	opt := check.Options{
		Target:            target,
		Namespaces:        cfg.namespaces.vals,
		ExcludeNamespaces: cfg.excludeNS.vals,
		ServerDryRun:      cfg.serverDryRun,
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	r := check.Run(ctx, check.Cluster{Client: cs, Metrics: apiserverMetrics{cs}}, checks, opt)

	if err := report.Write(stdout, r, format, report.Options{Quiet: cfg.quiet}); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	return exitCode(r.Overall, cfg.failOn)
}

func exitCode(overall check.Status, failOn string) int {
	switch {
	case overall == check.StatusFail:
		return exitFail
	case overall == check.StatusWarn && failOn == "warn":
		return exitFail
	default:
		return exitOK
	}
}

// apiserverMetrics reads /metrics from the API server through the client's
// authenticated transport.
type apiserverMetrics struct{ cs kubernetes.Interface }

func (m apiserverMetrics) Fetch(ctx context.Context) ([]byte, error) {
	return m.cs.CoreV1().RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
}

func newClient(kubeconfig, kubeContext string) (kubernetes.Interface, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	restCfg.UserAgent = "kube-upgrade-preflight/" + version
	return kubernetes.NewForConfig(restCfg)
}

func listChecks(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDESCRIPTION")
	for _, c := range check.All() {
		fmt.Fprintf(tw, "%s\t%s\n", c.Name(), c.Description())
	}
	return tw.Flush()
}

// csvFlag accepts a comma separated list and may be repeated.
type csvFlag struct{ vals []string }

func (c *csvFlag) String() string {
	if c == nil {
		return ""
	}
	return strings.Join(c.vals, ",")
}

func (c *csvFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.vals = append(c.vals, p)
		}
	}
	return nil
}
