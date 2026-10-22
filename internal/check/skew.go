package check

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/OTA-EITA/kube-upgrade-preflight/internal/kubever"
)

// maxKubeletSkew is how many minor versions a kubelet may lag behind the
// API server (Kubernetes version skew policy since v1.28).
const maxKubeletSkew = 3

// SkewCheck validates the requested upgrade path against the version skew policy:
// the control plane can only move one minor version at a time, and after the
// upgrade every kubelet must still be within the supported skew.
type SkewCheck struct{}

func (SkewCheck) Name() string { return "version-skew" }
func (SkewCheck) Description() string {
	return "Upgrade moves one minor version and every kubelet stays within the supported skew"
}

func (c SkewCheck) Run(ctx context.Context, cl Cluster, opt Options) ([]Finding, error) {
	info, err := cl.Client.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("get server version: %w", err)
	}
	current, err := kubever.Parse(info.GitVersion)
	if err != nil {
		return nil, err
	}
	target := opt.Target
	path := fmt.Sprintf("%s -> %s", current, target)

	var out []Finding
	switch step := current.MinorsBehind(target); {
	case current.Major != target.Major:
		out = append(out, fail(c.Name(), "control-plane", path+": major version upgrades are not supported"))
	case step < 0:
		out = append(out, fail(c.Name(), "control-plane", path+": target is older than the running control plane"))
	case step == 0:
		out = append(out, warn(c.Name(), "control-plane", path+": already on the target minor version"))
	case step > 1:
		out = append(out, fail(c.Name(), "control-plane", fmt.Sprintf("%s: skips %d minor versions; upgrade one minor at a time", path, step-1)))
	default:
		out = append(out, pass(c.Name(), "control-plane", path))
	}

	nodes, err := cl.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	for _, n := range nodes.Items {
		res := "node/" + n.Name
		kv, err := kubever.Parse(n.Status.NodeInfo.KubeletVersion)
		if err != nil {
			out = append(out, warn(c.Name(), res, err.Error()))
			continue
		}
		switch behind := kv.MinorsBehind(target); {
		case kv.Major != target.Major || behind < 0:
			out = append(out, fail(c.Name(), res, fmt.Sprintf("kubelet %s is newer than the target %s", kv, target)))
		case behind > maxKubeletSkew:
			out = append(out, fail(c.Name(), res, fmt.Sprintf("kubelet %s would be %d minors behind %s (max %d); upgrade this node first", kv, behind, target, maxKubeletSkew)))
		default:
			out = append(out, pass(c.Name(), res, fmt.Sprintf("kubelet %s is supported with %s", kv, target)))
		}
	}
	return out, nil
}
