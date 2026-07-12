package reconciler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

func TestNamespaceReconciler_ManagedMode_CreatesNamespace(t *testing.T) {
	client := fake.NewClientset()
	r := reconciler.NewNamespaceReconciler(client)

	err := r.Reconcile(t.Context(), hydraclient.Namespace{ID: "ns-1", K8sName: "prod", Mode: "managed"})
	require.NoError(t, err)

	got, err := client.CoreV1().Namespaces().Get(t.Context(), "prod", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "prod", got.Name)
}

func TestNamespaceReconciler_ImportedMode_DoesNotCreateMissingNamespace(t *testing.T) {
	client := fake.NewClientset()
	r := reconciler.NewNamespaceReconciler(client)

	err := r.Reconcile(t.Context(), hydraclient.Namespace{ID: "ns-1", K8sName: "existing-ns", Mode: "imported"})
	require.Error(t, err)

	_, getErr := client.CoreV1().Namespaces().Get(t.Context(), "existing-ns", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(getErr))
}

func TestNamespaceReconciler_ImportedMode_SucceedsWhenNamespaceExists(t *testing.T) {
	client := fake.NewClientset()
	_, err := client.CoreV1().Namespaces().Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-ns"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	r := reconciler.NewNamespaceReconciler(client)
	err = r.Reconcile(t.Context(), hydraclient.Namespace{ID: "ns-1", K8sName: "existing-ns", Mode: "imported"})
	require.NoError(t, err)
}

func TestNamespaceReconciler_WithQuotas_CreatesResourceQuota(t *testing.T) {
	client := fake.NewClientset()
	r := reconciler.NewNamespaceReconciler(client)

	ns := hydraclient.Namespace{
		ID: "ns-1", K8sName: "prod", Mode: "managed",
		Quotas: hydraclient.Quotas{LimitsCPU: "4", LimitsMemory: "8Gi", CountPods: 20},
	}
	require.NoError(t, r.Reconcile(t.Context(), ns))

	quota, err := client.CoreV1().ResourceQuotas("prod").Get(t.Context(), "hydra-quota", metav1.GetOptions{})
	require.NoError(t, err)
	cpu, mem := quota.Spec.Hard[corev1.ResourceLimitsCPU], quota.Spec.Hard[corev1.ResourceLimitsMemory]
	assert.Equal(t, "4", cpu.String())
	assert.Equal(t, "8Gi", mem.String())
}

func TestNamespaceReconciler_QuotaRemoved_DeletesResourceQuota(t *testing.T) {
	client := fake.NewClientset()
	r := reconciler.NewNamespaceReconciler(client)

	withQuota := hydraclient.Namespace{ID: "ns-1", K8sName: "prod", Mode: "managed", Quotas: hydraclient.Quotas{LimitsCPU: "4"}}
	require.NoError(t, r.Reconcile(t.Context(), withQuota))

	withoutQuota := hydraclient.Namespace{ID: "ns-1", K8sName: "prod", Mode: "managed"}
	require.NoError(t, r.Reconcile(t.Context(), withoutQuota))

	_, err := client.CoreV1().ResourceQuotas("prod").Get(t.Context(), "hydra-quota", metav1.GetOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestNamespaceReconciler_Reconcile_Idempotent(t *testing.T) {
	client := fake.NewClientset()
	r := reconciler.NewNamespaceReconciler(client)
	ns := hydraclient.Namespace{ID: "ns-1", K8sName: "prod", Mode: "managed", Quotas: hydraclient.Quotas{LimitsCPU: "4"}}

	require.NoError(t, r.Reconcile(t.Context(), ns))
	require.NoError(t, r.Reconcile(t.Context(), ns)) // must not error the second time

	quota, err := client.CoreV1().ResourceQuotas("prod").Get(t.Context(), "hydra-quota", metav1.GetOptions{})
	require.NoError(t, err)
	cpu := quota.Spec.Hard[corev1.ResourceLimitsCPU]
	assert.Equal(t, "4", cpu.String())
}
