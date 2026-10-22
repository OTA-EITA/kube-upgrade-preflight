package check

import "github.com/OTA-EITA/kube-upgrade-preflight/internal/kubever"

// Removal describes an API version that stops being served in RemovedIn.
// Source: https://kubernetes.io/docs/reference/using-api/deprecation-guide/
type Removal struct {
	APIVersion  string
	Kind        string
	RemovedIn   kubever.Version
	Replacement string
}

func removed(release, replacement, apiVersion string, kinds ...string) []Removal {
	v := kubever.MustParse(release)
	out := make([]Removal, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, Removal{APIVersion: apiVersion, Kind: k, RemovedIn: v, Replacement: replacement})
	}
	return out
}

var removalTable = func() map[string]Removal {
	var all []Removal
	add := func(rs ...[]Removal) {
		for _, r := range rs {
			all = append(all, r...)
		}
	}
	add(
		// v1.16
		removed("v1.16", "networking.k8s.io/v1", "extensions/v1beta1", "NetworkPolicy"),
		removed("v1.16", "apps/v1", "extensions/v1beta1", "DaemonSet", "Deployment", "ReplicaSet"),
		removed("v1.16", "apps/v1", "apps/v1beta1", "Deployment", "StatefulSet"),
		removed("v1.16", "apps/v1", "apps/v1beta2", "DaemonSet", "Deployment", "ReplicaSet", "StatefulSet"),
		removed("v1.16", "policy/v1beta1", "extensions/v1beta1", "PodSecurityPolicy"),

		// v1.22
		removed("v1.22", "admissionregistration.k8s.io/v1", "admissionregistration.k8s.io/v1beta1",
			"MutatingWebhookConfiguration", "ValidatingWebhookConfiguration"),
		removed("v1.22", "apiextensions.k8s.io/v1", "apiextensions.k8s.io/v1beta1", "CustomResourceDefinition"),
		removed("v1.22", "apiregistration.k8s.io/v1", "apiregistration.k8s.io/v1beta1", "APIService"),
		removed("v1.22", "authentication.k8s.io/v1", "authentication.k8s.io/v1beta1", "TokenReview"),
		removed("v1.22", "authorization.k8s.io/v1", "authorization.k8s.io/v1beta1",
			"SubjectAccessReview", "LocalSubjectAccessReview", "SelfSubjectAccessReview"),
		removed("v1.22", "certificates.k8s.io/v1", "certificates.k8s.io/v1beta1", "CertificateSigningRequest"),
		removed("v1.22", "coordination.k8s.io/v1", "coordination.k8s.io/v1beta1", "Lease"),
		removed("v1.22", "networking.k8s.io/v1", "extensions/v1beta1", "Ingress"),
		removed("v1.22", "networking.k8s.io/v1", "networking.k8s.io/v1beta1", "Ingress", "IngressClass"),
		removed("v1.22", "rbac.authorization.k8s.io/v1", "rbac.authorization.k8s.io/v1beta1",
			"ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding"),
		removed("v1.22", "scheduling.k8s.io/v1", "scheduling.k8s.io/v1beta1", "PriorityClass"),
		removed("v1.22", "storage.k8s.io/v1", "storage.k8s.io/v1beta1",
			"CSIDriver", "CSINode", "StorageClass", "VolumeAttachment"),

		// v1.25
		removed("v1.25", "batch/v1", "batch/v1beta1", "CronJob"),
		removed("v1.25", "discovery.k8s.io/v1", "discovery.k8s.io/v1beta1", "EndpointSlice"),
		removed("v1.25", "events.k8s.io/v1", "events.k8s.io/v1beta1", "Event"),
		removed("v1.25", "autoscaling/v2", "autoscaling/v2beta1", "HorizontalPodAutoscaler"),
		removed("v1.25", "policy/v1", "policy/v1beta1", "PodDisruptionBudget"),
		removed("v1.25", "Pod Security Admission (no direct replacement)", "policy/v1beta1", "PodSecurityPolicy"),
		removed("v1.25", "node.k8s.io/v1", "node.k8s.io/v1beta1", "RuntimeClass"),

		// v1.26
		removed("v1.26", "flowcontrol.apiserver.k8s.io/v1", "flowcontrol.apiserver.k8s.io/v1beta1",
			"FlowSchema", "PriorityLevelConfiguration"),
		removed("v1.26", "autoscaling/v2", "autoscaling/v2beta2", "HorizontalPodAutoscaler"),

		// v1.27
		removed("v1.27", "storage.k8s.io/v1", "storage.k8s.io/v1beta1", "CSIStorageCapacity"),

		// v1.29
		removed("v1.29", "flowcontrol.apiserver.k8s.io/v1", "flowcontrol.apiserver.k8s.io/v1beta2",
			"FlowSchema", "PriorityLevelConfiguration"),

		// v1.32
		removed("v1.32", "flowcontrol.apiserver.k8s.io/v1", "flowcontrol.apiserver.k8s.io/v1beta3",
			"FlowSchema", "PriorityLevelConfiguration"),
	)
	m := make(map[string]Removal, len(all))
	for _, r := range all {
		m[r.APIVersion+"/"+r.Kind] = r
	}
	return m
}()

// LookupRemoval returns the removal entry for an apiVersion and kind, if any.
func LookupRemoval(apiVersion, kind string) (Removal, bool) {
	r, ok := removalTable[apiVersion+"/"+kind]
	return r, ok
}
