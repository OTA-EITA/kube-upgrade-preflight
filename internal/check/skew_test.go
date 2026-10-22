package check

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func kubeletNode(name, kubelet string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: kubelet}},
	}
}

func TestSkewCheckControlPlanePath(t *testing.T) {
	cases := []struct {
		server, target string
		want           Status
		msg            string
	}{
		{"v1.34.1", "v1.35", StatusPass, "v1.34 -> v1.35"},
		{"v1.33.5-eks-1", "v1.35", StatusFail, "skips 1 minor"},
		{"v1.35.0", "v1.35", StatusWarn, "already on the target"},
		{"v1.35.0", "v1.34", StatusFail, "older than the running"},
	}
	for _, c := range cases {
		fs := runCheck(t, SkewCheck{}, Cluster{Client: newFake(c.server)}, testOptions(c.target))
		f := expectStatus(t, fs, "control-plane", c.want)
		if !strings.Contains(f.Message, c.msg) {
			t.Errorf("%s -> %s: message %q should contain %q", c.server, c.target, f.Message, c.msg)
		}
	}
}

func TestSkewCheckKubelets(t *testing.T) {
	cs := newFake("v1.34.1",
		kubeletNode("current", "v1.34.1"),
		kubeletNode("three-behind", "v1.32.9"),
		kubeletNode("four-behind", "v1.31.2"),
		kubeletNode("newer", "v1.36.0"),
	)
	fs := runCheck(t, SkewCheck{}, Cluster{Client: cs}, testOptions("v1.35"))
	expectStatus(t, fs, "node/current", StatusPass)
	expectStatus(t, fs, "node/three-behind", StatusPass)
	expectStatus(t, fs, "node/four-behind", StatusFail)
	expectStatus(t, fs, "node/newer", StatusFail)
}
