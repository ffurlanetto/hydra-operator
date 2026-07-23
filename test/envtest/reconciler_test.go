//go:build envtest

// Package envtest_test runs hydra-operator's reconcilers against a real
// kube-apiserver + etcd (no kubelet, no container runtime, no Knative
// controller — CRD schema validation and object persistence only), so
// reconciliation logic is exercised against genuine API server validation
// instead of the fake clientsets internal/reconciler's unit tests use.
// This catches, for example, a hydraclient.Container→Knative Service
// mapping that the fake clientset would silently accept but a real
// apiserver enforcing the Knative Serving CRD's structural schema would
// reject.
//
// Requires:
//   - KUBEBUILDER_ASSETS pointing at etcd/kube-apiserver binaries
//     (`make envtest-setup`, or `setup-envtest use` directly)
//   - Knative Serving CRDs in ./crds (fetched by CI — see
//     .github/workflows/e2e-kind.yml and docs/testing/e2e.md; not checked
//     in because they change with every Knative release)
//
// Not part of `go test ./...` or CI's default job — see `make test-envtest`.
package envtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes"
	knativeversioned "knative.dev/serving/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

func startEnv(t *testing.T) (corev1client.Interface, knativeversioned.Interface) {
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
	return core, knative
}

func TestNamespaceReconciler_AgainstRealAPIServer_CreatesNamespace(t *testing.T) {
	core, _ := startEnv(t)
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
	core, knative := startEnv(t)
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
