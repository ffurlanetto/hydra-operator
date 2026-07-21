//go:build envtest

// Package envtest_test runs hydra-operator's reconcilers against a real
// kube-apiserver + etcd (no kubelet, no container runtime, no Knative or
// OpenShift controller — CRD schema validation and object persistence only),
// so reconciliation logic is exercised against genuine API server validation
// instead of the fake clientsets internal/reconciler's unit tests use.
// This catches, for example, a hydraclient.Container→Knative Service
// mapping that the fake clientset would silently accept but a real
// apiserver enforcing the Knative Serving CRD's structural schema would
// reject — or a Route spec value (e.g. an invalid TLS termination) the
// OpenShift Route fake clientset never validates against its CRD's enum.
//
// Requires:
//   - KUBEBUILDER_ASSETS pointing at etcd/kube-apiserver binaries
//     (`make envtest-setup`, or `setup-envtest use` directly)
//   - Knative Serving CRDs and the OpenShift Route CRD in ./crds (fetched by
//     CI and `make test-envtest` — see .github/workflows/e2e-kind.yml and
//     docs/testing/e2e.md; not checked in). The Route CRD is fetched from
//     github.com/openshift/api at the exact commit this repo's go.mod
//     already pins (see Makefile's test-envtest target), so it always
//     matches the routev1 Go types this repo compiles against — unlike
//     Knative's floating `/releases/latest/download` URL, this needs no
//     separate version tracking.
//
// Not part of `go test ./...` or CI's default job — see `make test-envtest`.
package envtest_test

import (
	"context"
	"testing"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	routeversioned "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes"
	knativeversioned "knative.dev/serving/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

func startEnv(t *testing.T) (corev1client.Interface, knativeversioned.Interface, routeversioned.Interface) {
	t.Helper()
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{"crds"},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	core, err := corev1client.NewForConfig(cfg)
	require.NoError(t, err)
	knative, err := knativeversioned.NewForConfig(cfg)
	require.NoError(t, err)
	routes, err := routeversioned.NewForConfig(cfg)
	require.NoError(t, err)
	return core, knative, routes
}

func TestNamespaceReconciler_AgainstRealAPIServer_CreatesNamespace(t *testing.T) {
	core, _, _ := startEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := reconciler.NewNamespaceReconciler(core)
	require.NoError(t, r.Reconcile(ctx, hydraclient.Namespace{
		ID: "ns-1", K8sName: "prod-envtest", Mode: "managed",
		Quotas: hydraclient.Quotas{LimitsCPU: "4", LimitsMemory: "8Gi"},
	}))

	ns, err := core.CoreV1().Namespaces().Get(ctx, "prod-envtest", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "prod-envtest", ns.Name)

	quota, err := core.CoreV1().ResourceQuotas("prod-envtest").Get(ctx, "hydra-quota", metav1.GetOptions{})
	require.NoError(t, err)
	cpu := quota.Spec.Hard["limits.cpu"]
	require.Equal(t, "4", cpu.String())
}

func TestContainerReconciler_AgainstRealAPIServer_SchemaAcceptsGeneratedService(t *testing.T) {
	core, knative, _ := startEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsReconciler := reconciler.NewNamespaceReconciler(core)
	require.NoError(t, nsReconciler.Reconcile(ctx, hydraclient.Namespace{ID: "ns-1", K8sName: "prod-envtest2", Mode: "managed"}))

	containerID := "11111111-1111-1111-1111-111111111111"
	r := reconciler.NewContainerReconciler(knative)
	status, err := r.Reconcile(ctx, hydraclient.Container{
		ID:               containerID,
		NamespaceK8sName: "prod-envtest2",
		Definition: hydraclient.Definition{
			Image: "gcr.io/knative-samples/helloworld-go", CPULimit: "250m", MemoryLimit: "256Mi",
			HealthCheckPath: "/healthz",
		},
		Scaling: hydraclient.Scaling{MinScale: 0, MaxScale: 3},
		// No RuntimeClassName: envtest has no such RuntimeClass object, and
		// setting one that doesn't exist would only fail if a real kubelet
		// tried to schedule the pod (it never does here).
	})
	require.NoError(t, err)
	// No real Knative controller runs against envtest, so this never
	// progresses past "deploying" — the point of this test is that the CRD
	// accepted the object at all, not that it becomes Ready.
	require.Equal(t, "deploying", status.Status)

	svc, err := knative.ServingV1().Services("prod-envtest2").Get(ctx, reconciler.KsvcNameFor(containerID), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "gcr.io/knative-samples/helloworld-go", svc.Spec.Template.Spec.Containers[0].Image)
	require.Equal(t, "/healthz", svc.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Path)
}

func TestRouteReconciler_AgainstRealAPIServer_SchemaAcceptsCreatedRoute(t *testing.T) {
	core, _, routes := startEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsReconciler := reconciler.NewNamespaceReconciler(core)
	require.NoError(t, nsReconciler.Reconcile(ctx, hydraclient.Namespace{ID: "ns-1", K8sName: "prod-envtest3", Mode: "managed"}))

	containerID := "22222222-2222-2222-2222-222222222222"
	r := reconciler.NewRouteReconciler(routes)
	c := hydraclient.Container{
		ID:                  containerID,
		NamespaceK8sName:    "prod-envtest3",
		NeedsOpenShiftRoute: true,
	}
	_, err := r.Reconcile(ctx, c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(containerID)
	route, err := routes.RouteV1().Routes("prod-envtest3").Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "Service", route.Spec.To.Kind)
	require.Equal(t, name, route.Spec.To.Name)
	require.NotNil(t, route.Spec.TLS)
	require.Equal(t, routev1.TLSTerminationEdge, route.Spec.TLS.Termination)

	// Status is a separate subresource on a real API server (unlike some
	// fake clientsets); simulate the router admitting the route via
	// UpdateStatus, then reconcile again to confirm the reconciler's
	// endpoint extraction round-trips against genuine subresource semantics.
	route.Status.Ingress = []routev1.RouteIngress{{Host: "agent.apps.envtest.example.com"}}
	_, err = routes.RouteV1().Routes("prod-envtest3").UpdateStatus(ctx, route, metav1.UpdateOptions{})
	require.NoError(t, err)

	endpoint, err := r.Reconcile(ctx, c)
	require.NoError(t, err)
	require.Equal(t, "https://agent.apps.envtest.example.com", endpoint)
}

func TestRouteReconciler_AgainstRealAPIServer_RejectsInvalidTLSTermination(t *testing.T) {
	core, _, routes := startEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsReconciler := reconciler.NewNamespaceReconciler(core)
	require.NoError(t, nsReconciler.Reconcile(ctx, hydraclient.Namespace{ID: "ns-1", K8sName: "prod-envtest4", Mode: "managed"}))

	// The Route fake clientset (internal/reconciler/route_test.go) accepts
	// any string here; the real Route CRD constrains spec.tls.termination to
	// an enum (edge|reencrypt|passthrough). This is exactly the class of bug
	// tier 1 (fake clientset) unit tests structurally cannot catch.
	invalid := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-route", Namespace: "prod-envtest4"},
		Spec: routev1.RouteSpec{
			To:  routev1.RouteTargetReference{Kind: "Service", Name: "whatever"},
			TLS: &routev1.TLSConfig{Termination: "bogus-termination"},
		},
	}
	_, err := routes.RouteV1().Routes("prod-envtest4").Create(ctx, invalid, metav1.CreateOptions{})
	require.Error(t, err)
	require.True(t, apierrors.IsInvalid(err), "expected schema validation error, got: %v", err)
}
