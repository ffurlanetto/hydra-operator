package reconciler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	servingv1beta1 "knative.dev/serving/pkg/apis/serving/v1beta1"
	knativefake "knative.dev/serving/pkg/client/clientset/versioned/fake"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

func TestDomainReconciler_NoCustomDomains_ReturnsEmpty(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewDomainReconciler(client)

	c := baseContainer()
	statuses := r.Reconcile(t.Context(), c)
	assert.Empty(t, statuses)
}

func TestDomainReconciler_NewDomain_CreatesDomainMappingPointingAtService(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewDomainReconciler(client)

	c := baseContainer()
	c.CustomDomains = []hydraclient.CustomDomain{{ID: "domain-1", Hostname: "app.example.com"}}
	statuses := r.Reconcile(t.Context(), c)

	require.Len(t, statuses, 1)
	assert.Equal(t, "domain-1", statuses[0].ID)
	assert.Equal(t, "app.example.com", statuses[0].Hostname)
	// Freshly created, Knative's controller hasn't marked it Ready yet.
	assert.Equal(t, "error", statuses[0].Status)

	mapping, err := client.ServingV1beta1().DomainMappings("prod").Get(t.Context(), "app.example.com", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, reconciler.KsvcNameFor(c.ID), mapping.Spec.Ref.Name)
	assert.Equal(t, "Service", mapping.Spec.Ref.Kind)
}

func TestDomainReconciler_NewDomain_WithoutID_StatusHasEmptyID(t *testing.T) {
	// Covers a hostname arriving with no domain ID at all — e.g. an older
	// Hydra API that hasn't been updated to send one yet. The DomainMapping
	// is still reconciled normally; only the returned DomainStatus.ID is
	// empty, which is the manager's signal to skip the status callback.
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewDomainReconciler(client)

	c := baseContainer()
	c.CustomDomains = []hydraclient.CustomDomain{{Hostname: "app.example.com"}}
	statuses := r.Reconcile(t.Context(), c)

	require.Len(t, statuses, 1)
	assert.Empty(t, statuses[0].ID)
	assert.Equal(t, "app.example.com", statuses[0].Hostname)
}

func TestDomainReconciler_ReadyDomainMapping_ReportsActive(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewDomainReconciler(client)

	c := baseContainer()
	c.CustomDomains = []hydraclient.CustomDomain{{ID: "domain-1", Hostname: "app.example.com"}}
	r.Reconcile(t.Context(), c)

	mapping, err := client.ServingV1beta1().DomainMappings("prod").Get(t.Context(), "app.example.com", metav1.GetOptions{})
	require.NoError(t, err)
	mapping.Status.Status = duckv1.Status{
		Conditions: duckv1.Conditions{{
			Type:   servingv1beta1.DomainMappingConditionReady,
			Status: corev1.ConditionTrue,
		}},
	}
	_, err = client.ServingV1beta1().DomainMappings("prod").UpdateStatus(t.Context(), mapping, metav1.UpdateOptions{})
	require.NoError(t, err)

	statuses := r.Reconcile(t.Context(), c)
	require.Len(t, statuses, 1)
	assert.Equal(t, "active", statuses[0].Status)
}

func TestDomainReconciler_MultipleHostnames_ReconciledIndependently(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewDomainReconciler(client)

	c := baseContainer()
	c.CustomDomains = []hydraclient.CustomDomain{
		{ID: "domain-1", Hostname: "app.example.com"},
		{ID: "domain-2", Hostname: "api.example.com"},
	}
	statuses := r.Reconcile(t.Context(), c)

	require.Len(t, statuses, 2)
	_, err := client.ServingV1beta1().DomainMappings("prod").Get(t.Context(), "app.example.com", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = client.ServingV1beta1().DomainMappings("prod").Get(t.Context(), "api.example.com", metav1.GetOptions{})
	require.NoError(t, err)
}
