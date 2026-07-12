//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

// TestE2E_RouteReconciler_OnOpenShift_RealRouterAdmitsRoute only runs against
// a cluster that actually has the OpenShift Route API (real OpenShift Local/
// CRC via scripts/e2e-local.sh) — it skips cleanly everywhere else,
// including the kind cluster used in CI, which has no route.openshift.io
// group at all. This is the one scenario in this suite that a plain kind
// cluster can never exercise: whether OpenShift's real router admits the
// Route this operator builds and assigns it a routable host.
func TestE2E_RouteReconciler_OnOpenShift_RealRouterAdmitsRoute(t *testing.T) {
	clients := requireCluster(t)
	ctx := context.Background()

	detector := capabilities.New(clients.Core, 10*time.Second)
	result, err := detector.Detect(ctx)
	require.NoError(t, err)
	if !result.Capabilities.OpenShiftAvailable {
		t.Skip("cluster has no route.openshift.io API — skipping (needs real OpenShift Local/CRC, see docs/testing/e2e.md)")
	}

	namespace := newTestNamespace(t, clients)
	containerID := "e2e-route"

	containerReconciler := reconciler.NewContainerReconciler(clients.Knative)
	container := hydraclient.Container{
		ID:               containerID,
		NamespaceK8sName: namespace,
		Definition: hydraclient.Definition{
			Image: helloworldImage, CPULimit: "100m", MemoryLimit: "128Mi",
		},
		Scaling:             hydraclient.Scaling{MinScale: 0, MaxScale: 1},
		RestartGeneration:   1,
		NeedsOpenShiftRoute: true,
	}
	_, err = containerReconciler.Reconcile(ctx, container)
	require.NoError(t, err)
	waitForReady(t, clients.Knative, namespace, reconciler.KsvcNameFor(containerID))

	routeReconciler := reconciler.NewRouteReconciler(clients.Route)
	var endpoint string
	require.Eventually(t, func() bool {
		endpoint, err = routeReconciler.Reconcile(ctx, container)
		return err == nil && endpoint != ""
	}, 2*time.Minute, 2*time.Second, "real OpenShift router should admit the Route and assign a host")
	require.Contains(t, endpoint, "https://")

	route, err := clients.Route.RouteV1().Routes(namespace).Get(ctx, reconciler.KsvcNameFor(containerID), metav1.GetOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, route.Status.Ingress)
	require.Equal(t, "edge", string(route.Spec.TLS.Termination))
}
