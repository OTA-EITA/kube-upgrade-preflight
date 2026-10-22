package check

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// helmSecret encodes a release the same way Helm 3 stores it.
func helmSecret(t *testing.T, ns, name string, rev int, manifest string) *corev1.Secret {
	t.Helper()
	js, err := json.Marshal(map[string]any{
		"name": name, "namespace": ns, "version": rev, "manifest": manifest,
		"info": map[string]string{"status": "deployed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(js); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, rev),
			Labels:    map[string]string{"owner": "helm", "name": name},
		},
		Type: helmSecretType,
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))},
	}
}

const oldManifest = `---
# Source: legacy/templates/ingress.yaml
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: legacy
---
apiVersion: policy/v1beta1
kind: PodDisruptionBudget
metadata:
  name: legacy
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: legacy
`

const fixedManifest = `---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: legacy
`

func TestHelmCheck(t *testing.T) {
	cs := newFake("v1.34.1",
		helmSecret(t, "web", "legacy", 1, oldManifest),
		helmSecret(t, "web", "modern", 3, fixedManifest),
	)
	fs := runCheck(t, HelmCheck{}, Cluster{Client: cs}, testOptions("v1.35"))

	legacy := findingsFor(fs, "helm/web/legacy (rev 1)")
	if len(legacy) != 2 {
		t.Fatalf("want 2 removed APIs in legacy, got %+v", legacy)
	}
	for _, f := range legacy {
		if f.Status != StatusFail || !strings.Contains(f.Message, "migrate to") {
			t.Errorf("unexpected finding %+v", f)
		}
	}
	expectStatus(t, fs, "helm/web/modern (rev 3)", StatusPass)
}

func TestHelmCheckUsesLatestRevision(t *testing.T) {
	cs := newFake("v1.34.1",
		helmSecret(t, "web", "app", 1, oldManifest),
		helmSecret(t, "web", "app", 2, fixedManifest),
	)
	fs := runCheck(t, HelmCheck{}, Cluster{Client: cs}, testOptions("v1.35"))
	if len(fs) != 1 || fs[0].Resource != "helm/web/app (rev 2)" || fs[0].Status != StatusPass {
		t.Fatalf("only the latest revision matters, got %+v", fs)
	}
}

func TestHelmCheckTargetBeforeRemoval(t *testing.T) {
	cs := newFake("v1.20.0", helmSecret(t, "web", "legacy", 1, oldManifest))
	fs := runCheck(t, HelmCheck{}, Cluster{Client: cs}, testOptions("v1.21"))
	expectStatus(t, fs, "helm/web/legacy (rev 1)", StatusPass)
}

func TestHelmCheckUndecodableRelease(t *testing.T) {
	bad := helmSecret(t, "web", "broken", 1, "")
	bad.Data["release"] = []byte("!!!")
	fs := runCheck(t, HelmCheck{}, Cluster{Client: newFake("v1.34.1", bad)}, testOptions("v1.35"))
	expectStatus(t, fs, "secret/web/sh.helm.release.v1.broken.v1", StatusWarn)
}
