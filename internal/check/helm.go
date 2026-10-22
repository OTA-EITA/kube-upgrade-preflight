package check

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// HelmCheck decodes the latest revision of every Helm 3 release and looks for
// manifests that use an API version removed by the target release. Such a
// release keeps working until the upgrade, then `helm upgrade` fails because
// Helm can no longer read its own previous manifest.
type HelmCheck struct{}

func (HelmCheck) Name() string { return "helm-releases" }
func (HelmCheck) Description() string {
	return "Helm releases do not contain manifests with API versions removed by the target release"
}

const (
	helmSecretType = "helm.sh/release.v1" // #nosec G101 -- a Secret type name, not a credential
	// maxReleaseSize guards against decompression bombs in release secrets.
	maxReleaseSize = 32 << 20
)

type helmRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
	Manifest  string `json:"manifest"`
	Info      struct {
		Status string `json:"status"`
	} `json:"info"`
}

type manifestHeader struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
}

func (c HelmCheck) Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error) {
	secrets, err := cl.Client.CoreV1().Secrets(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm"})
	if err != nil {
		return nil, fmt.Errorf("list helm release secrets: %w", err)
	}

	latest := map[string]helmRelease{}
	var out []Finding
	for _, s := range secrets.Items {
		if string(s.Type) != helmSecretType || !opt.inScope(s.Namespace) {
			continue
		}
		rel, err := decodeRelease(s.Data["release"])
		if err != nil {
			out = append(out, warn(c.Name(), "secret/"+s.Namespace+"/"+s.Name, "cannot decode Helm release: "+err.Error()))
			continue
		}
		key := rel.Namespace + "/" + rel.Name
		if prev, ok := latest[key]; !ok || rel.Version > prev.Version {
			latest[key] = rel
		}
	}

	for key, rel := range latest {
		res := fmt.Sprintf("helm/%s (rev %d)", key, rel.Version)
		problems := 0
		for _, h := range manifestHeaders(rel.Manifest) {
			r, ok := LookupRemoval(h.APIVersion, h.Kind)
			if !ok || opt.Target.Less(r.RemovedIn) {
				continue
			}
			problems++
			out = append(out, fail(c.Name(), res, fmt.Sprintf("%s %s uses %s, removed in %s; migrate to %s",
				h.Kind, h.Metadata.Name, h.APIVersion, r.RemovedIn, r.Replacement)))
		}
		if problems == 0 {
			out = append(out, pass(c.Name(), res, "no removed API versions in the release manifest"))
		}
	}
	return out, nil
}

// decodeRelease reverses Helm 3's storage encoding: base64(gzip(json)).
func decodeRelease(data []byte) (helmRelease, error) {
	var rel helmRelease
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return rel, fmt.Errorf("base64: %w", err)
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return rel, fmt.Errorf("gzip: %w", err)
		}
		raw, err = io.ReadAll(io.LimitReader(zr, maxReleaseSize))
		if cerr := zr.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return rel, fmt.Errorf("gzip: %w", err)
		}
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		return rel, fmt.Errorf("json: %w", err)
	}
	return rel, nil
}

var docSeparator = regexp.MustCompile(`(?m)^---\s*$`)

// manifestHeaders returns apiVersion, kind and name of each YAML document.
func manifestHeaders(manifest string) []manifestHeader {
	var out []manifestHeader
	for _, doc := range docSeparator.Split(manifest, -1) {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var h manifestHeader
		if err := yaml.Unmarshal([]byte(doc), &h); err != nil || h.Kind == "" {
			continue
		}
		out = append(out, h)
	}
	return out
}
