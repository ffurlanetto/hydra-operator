package reconciler

import "strings"

// KsvcNameFor derives a deterministic, K8s-safe (RFC 1123 DNS subdomain)
// Knative Service name from a Hydra container ID (a UUID string).
//
// desiredStateContainer (Hydra's payload) carries only the container's
// UUID, not its human-readable name/K8sName — so hydra-operator can't
// reproduce Hydra's own "hydra-<slug>" naming convention (domain.K8sNameFor)
// and instead uses this UUID-derived name consistently for every object it
// owns for the container (Service, Route, DomainMapping). It is never
// derived from anything user-controlled, so no further sanitization is
// needed beyond lowercasing.
func KsvcNameFor(containerID string) string {
	return "ksvc-" + strings.ToLower(containerID)
}
