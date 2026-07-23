package reconciler_test

import (
	"testing"

	routev1 "github.com/openshift/api/route/v1"
	routefake "github.com/openshift/client-go/route/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

func TestRouteReconciler_NeedsOpenShiftRoute_CreatesRoute(t *testing.T) {
	client := routefake.NewSimpleClientset()
	r := reconciler.NewRouteReconciler(client)

	c := baseContainer()
	c.NeedsOpenShiftRoute = true
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	route, err := client.RouteV1().Routes("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, name, route.Spec.To.Name)
	assert.Equal(t, "Service", route.Spec.To.Kind)
	require.NotNil(t, route.Spec.TLS)
	assert.Equal(t, routev1.TLSTerminationEdge, route.Spec.TLS.Termination)
}

func TestRouteReconciler_NotNeeded_NoRouteCreated(t *testing.T) {
	client := routefake.NewSimpleClientset()
	r := reconciler.NewRouteReconciler(client)

	c := baseContainer()
	c.NeedsOpenShiftRoute = false
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	_, err = client.RouteV1().Routes("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestRouteReconciler_NoLongerNeeded_DeletesExistingRoute(t *testing.T) {
	client := routefake.NewSimpleClientset()
	r := reconciler.NewRouteReconciler(client)

	c := baseContainer()
	c.NeedsOpenShiftRoute = true
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	c.NeedsOpenShiftRoute = false
	_, err = r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	_, err = client.RouteV1().Routes("prod").Get(t.Context(), name, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestRouteReconciler_EndpointExtractedFromIngressHost(t *testing.T) {
	client := routefake.NewSimpleClientset()
	r := reconciler.NewRouteReconciler(client)

	c := baseContainer()
	c.NeedsOpenShiftRoute = true
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	route, err := client.RouteV1().Routes("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	route.Status.Ingress = []routev1.RouteIngress{{Host: "agent.apps.example.com"}}
	_, err = client.RouteV1().Routes("prod").UpdateStatus(t.Context(), route, metav1.UpdateOptions{})
	require.NoError(t, err)

	endpoint, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)
	assert.Equal(t, "https://agent.apps.example.com", endpoint)
}
