//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/k8sclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

// allowedRegistryPrefix matches helloworldImage, so pods that need to stay
// compliant with the allowed-registry Constraint (while still violating a
// different policy on purpose) can use it.
const allowedRegistryPrefix = "gcr.io/knative-samples/"

// disallowedRegistryImage is a syntactically valid image reference from a
// registry that isn't in the allowlist configured below. Gatekeeper's
// K8sAllowedRepos constraint evaluates the image string at admission time —
// the image is never actually pulled, since the Pod is rejected before
// scheduling — so this doesn't need to resolve to a real, pullable image.
const disallowedRegistryImage = "docker.io/library/nginx:latest"

// requireGatekeeper skips the test cleanly on any cluster that doesn't have
// Gatekeeper's admission API installed — mirrors requireCluster/the
// OpenShift capability guard in openshift_route_test.go, rather than failing
// outright against a cluster that predates scripts/e2e-local.sh's Gatekeeper
// install step.
func requireGatekeeper(t *testing.T, clients *k8sclient.Clients) {
	t.Helper()
	detector := capabilities.New(clients.Core, 10*time.Second)
	result, err := detector.Detect(context.Background())
	require.NoError(t, err)
	if !result.Capabilities.GatekeeperAvailable {
		t.Skip("cluster has no constraints.gatekeeper.sh API — skipping (install Gatekeeper, e.g. scripts/e2e-local.sh, see docs/testing/e2e.md)")
	}
}

// compliantResources satisfies K8sRequiredResourceLimits, so a pod built
// with them only trips whichever other constraint a given test scenario
// intends to exercise.
func compliantResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resourceQuantity("100m"),
			corev1.ResourceMemory: resourceQuantity("128Mi"),
		},
	}
}

// testPod builds a minimal single-container Pod spec for the admission
// scenarios below. namespace/name are set by the caller; the container is
// otherwise left to the test case to make non-compliant with exactly one
// policy at a time, so a rejection can be attributed to the Rego package
// meant to cause it.
func testPod(namespace, name string, container corev1.Container) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{container}},
	}
}

// TestE2E_PolicyReconciler_RealGatekeeper_RejectsViolatingPods reconciles
// PolicyReconciler's real ConstraintTemplates/Constraints (ADR-026 MVP) and
// then proves the actual Rego bodies in internal/reconciler/policy.go —
// requiredResourceLimitsRego, blockPrivilegedRego, allowedReposRego — are
// enforced by a real Gatekeeper admission webhook, not merely persisted as
// K8s objects the way internal/reconciler/policy_test.go's fake-dynamic-
// client unit tests already cover.
func TestE2E_PolicyReconciler_RealGatekeeper_RejectsViolatingPods(t *testing.T) {
	clients := requireCluster(t)
	requireGatekeeper(t, clients)
	ctx := context.Background()
	namespace := newTestNamespace(t, clients)

	policyReconciler := reconciler.NewPolicyReconciler(clients.Dynamic)
	policy := hydraclient.SecurityPolicy{AllowedRegistries: []string{allowedRegistryPrefix}}

	// Gatekeeper generates each Constraint kind's CRD (e.g.
	// k8sblockprivileged.constraints.gatekeeper.sh) asynchronously after its
	// ConstraintTemplate is created — PolicyReconciler.Reconcile creates the
	// Constraint in the same call, which can race ahead of that CRD existing
	// on a freshly-installed Gatekeeper. Retry until it succeeds rather than
	// asserting success on the first attempt.
	require.Eventually(t, func() bool {
		return policyReconciler.Reconcile(ctx, policy) == nil
	}, 2*time.Minute, 3*time.Second, "PolicyReconciler.Reconcile should eventually succeed once Gatekeeper registers the Constraint CRDs")

	cases := []struct {
		name          string
		container     corev1.Container
		wantMsgSubstr string
	}{
		{
			name: "privileged container is rejected by blockPrivilegedRego",
			container: corev1.Container{
				Name:            "c",
				Image:           helloworldImage,
				Resources:       compliantResources(),
				SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
			},
			wantMsgSubstr: "must not set privileged: true",
		},
		{
			name: "missing resource limits is rejected by requiredResourceLimitsRego",
			container: corev1.Container{
				Name:  "c",
				Image: helloworldImage,
			},
			wantMsgSubstr: "has no cpu limit",
		},
		{
			name: "disallowed registry is rejected by allowedReposRego",
			container: corev1.Container{
				Name:      "c",
				Image:     disallowedRegistryImage,
				Resources: compliantResources(),
			},
			wantMsgSubstr: "not in the allowed list",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			podName := fmt.Sprintf("e2e-policy-violation-%d", time.Now().UnixNano())
			var lastErr error
			require.Eventually(t, func() bool {
				_, err := clients.Core.CoreV1().Pods(namespace).Create(ctx, testPod(namespace, podName, tc.container), metav1.CreateOptions{})
				if err == nil {
					// Shouldn't happen once Gatekeeper is enforcing — clean up
					// the accidental object so it doesn't linger and confuse a
					// re-run against a reused namespace.
					_ = clients.Core.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
					lastErr = nil
					return false
				}
				lastErr = err
				return strings.Contains(err.Error(), tc.wantMsgSubstr)
			}, 2*time.Minute, 3*time.Second, "real Gatekeeper webhook should reject the Pod with a message containing %q", tc.wantMsgSubstr)
			require.Errorf(t, lastErr, "pod creation should have been denied")
		})
	}
}

// TestE2E_PolicyReconciler_RealGatekeeper_AdmitsCompliantPod is the positive
// control for the rejection scenarios above: a Pod that satisfies all three
// Constraints must still be admitted. Without this, a misconfigured webhook
// that denies everything (e.g. fail-closed on an unrelated error) would make
// the rejection tests above pass for the wrong reason.
func TestE2E_PolicyReconciler_RealGatekeeper_AdmitsCompliantPod(t *testing.T) {
	clients := requireCluster(t)
	requireGatekeeper(t, clients)
	ctx := context.Background()
	namespace := newTestNamespace(t, clients)

	policyReconciler := reconciler.NewPolicyReconciler(clients.Dynamic)
	policy := hydraclient.SecurityPolicy{AllowedRegistries: []string{allowedRegistryPrefix}}
	require.Eventually(t, func() bool {
		return policyReconciler.Reconcile(ctx, policy) == nil
	}, 2*time.Minute, 3*time.Second, "PolicyReconciler.Reconcile should eventually succeed once Gatekeeper registers the Constraint CRDs")

	podName := fmt.Sprintf("e2e-policy-compliant-%d", time.Now().UnixNano())
	compliant := testPod(namespace, podName, corev1.Container{
		Name:      "c",
		Image:     helloworldImage,
		Resources: compliantResources(),
	})

	require.Eventually(t, func() bool {
		_, err := clients.Core.CoreV1().Pods(namespace).Create(ctx, compliant, metav1.CreateOptions{})
		return err == nil
	}, 2*time.Minute, 3*time.Second, "a Pod compliant with all three Constraints should be admitted by the real Gatekeeper webhook")

	t.Cleanup(func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = clients.Core.CoreV1().Pods(namespace).Delete(delCtx, podName, metav1.DeleteOptions{})
	})
}

func boolPtr(b bool) *bool { return &b }
