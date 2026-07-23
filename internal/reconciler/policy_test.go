package reconciler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

var (
	constraintTemplateGVR = schema.GroupVersionResource{Group: "templates.gatekeeper.sh", Version: "v1", Resource: "constrainttemplates"}
	allowedReposGVR       = schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: "k8sallowedrepos"}
	requiredLimitsGVR     = schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: "k8srequiredresourcelimits"}
	blockPrivilegedGVR    = schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: "k8sblockprivileged"}
)

func newFakeDynamicClient() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
}

func TestPolicyReconciler_Reconcile_CreatesConstraintTemplates(t *testing.T) {
	client := newFakeDynamicClient()
	r := reconciler.NewPolicyReconciler(client)

	err := r.Reconcile(context.Background(), hydraclient.SecurityPolicy{})
	require.NoError(t, err)

	for _, name := range []string{"hydra-required-resource-limits", "hydra-block-privileged", "hydra-allowed-repos"} {
		obj, err := client.Resource(constraintTemplateGVR).Get(context.Background(), name, metav1.GetOptions{})
		require.NoError(t, err, "template %s should exist", name)
		assert.Equal(t, "ConstraintTemplate", obj.GetKind())
	}
}

func TestPolicyReconciler_Reconcile_AlwaysEnforcesResourceLimitsAndPrivileged(t *testing.T) {
	client := newFakeDynamicClient()
	r := reconciler.NewPolicyReconciler(client)

	err := r.Reconcile(context.Background(), hydraclient.SecurityPolicy{})
	require.NoError(t, err)

	_, err = client.Resource(requiredLimitsGVR).Get(context.Background(), "hydra-required-resource-limits", metav1.GetOptions{})
	assert.NoError(t, err)
	_, err = client.Resource(blockPrivilegedGVR).Get(context.Background(), "hydra-block-privileged", metav1.GetOptions{})
	assert.NoError(t, err)
}

func TestPolicyReconciler_Reconcile_EmptyAllowlist_DoesNotCreateAllowedReposConstraint(t *testing.T) {
	client := newFakeDynamicClient()
	r := reconciler.NewPolicyReconciler(client)

	err := r.Reconcile(context.Background(), hydraclient.SecurityPolicy{})
	require.NoError(t, err)

	_, err = client.Resource(allowedReposGVR).Get(context.Background(), "hydra-allowed-repos", metav1.GetOptions{})
	assert.Error(t, err, "no allowlist configured should mean no Constraint, not one that rejects everything")
}

func TestPolicyReconciler_Reconcile_WithAllowlist_CreatesAllowedReposConstraintWithRepos(t *testing.T) {
	client := newFakeDynamicClient()
	r := reconciler.NewPolicyReconciler(client)

	err := r.Reconcile(context.Background(), hydraclient.SecurityPolicy{
		AllowedRegistries: []string{"registry.example.com/", "gcr.io/knative-samples/"},
	})
	require.NoError(t, err)

	obj, err := client.Resource(allowedReposGVR).Get(context.Background(), "hydra-allowed-repos", metav1.GetOptions{})
	require.NoError(t, err)

	repos, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "parameters", "repos")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []string{"registry.example.com/", "gcr.io/knative-samples/"}, repos)
}

func TestPolicyReconciler_Reconcile_AllowlistRemoved_DeletesExistingConstraint(t *testing.T) {
	client := newFakeDynamicClient()
	r := reconciler.NewPolicyReconciler(client)

	require.NoError(t, r.Reconcile(context.Background(), hydraclient.SecurityPolicy{
		AllowedRegistries: []string{"registry.example.com/"},
	}))
	_, err := client.Resource(allowedReposGVR).Get(context.Background(), "hydra-allowed-repos", metav1.GetOptions{})
	require.NoError(t, err)

	require.NoError(t, r.Reconcile(context.Background(), hydraclient.SecurityPolicy{}))
	_, err = client.Resource(allowedReposGVR).Get(context.Background(), "hydra-allowed-repos", metav1.GetOptions{})
	assert.Error(t, err)
}

func TestPolicyReconciler_Reconcile_Idempotent(t *testing.T) {
	client := newFakeDynamicClient()
	r := reconciler.NewPolicyReconciler(client)

	policy := hydraclient.SecurityPolicy{AllowedRegistries: []string{"registry.example.com/"}}
	require.NoError(t, r.Reconcile(context.Background(), policy))
	require.NoError(t, r.Reconcile(context.Background(), policy))

	obj, err := client.Resource(allowedReposGVR).Get(context.Background(), "hydra-allowed-repos", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "hydra-allowed-repos", obj.GetName())
}
