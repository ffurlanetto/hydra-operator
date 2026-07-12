package reconciler

import (
	"context"
	"fmt"

	routev1 "github.com/openshift/api/route/v1"
	routeversioned "github.com/openshift/client-go/route/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// RouteReconciler manages an OpenShift Route exposing a Knative Service,
// used only when the cluster lacks Kourier/Gateway API (HYDRA-052) — the
// operator's own capability detection (internal/capabilities) plus Hydra's
// desired-state flag (Container.NeedsOpenShiftRoute) both gate this.
type RouteReconciler struct {
	routes routeversioned.Interface
}

// NewRouteReconciler builds a RouteReconciler using routes.
func NewRouteReconciler(routes routeversioned.Interface) *RouteReconciler {
	return &RouteReconciler{routes: routes}
}

// Reconcile ensures (or removes) a Route named after the container's
// Knative Service, targeting that Service by name — Knative's serving
// controller creates a same-named K8s Service alongside every ksvc as its
// routing target, regardless of ingress implementation. No target Port is
// set: the Route targets the Service's first exposed port, since the exact
// port name Knative's generated Service uses isn't part of Hydra's
// desired-state contract. TLS termination is always "edge" (HYDRA-052 AC):
// the router terminates TLS and forwards plaintext to the backend, matching
// Knative's own internal transport.
func (r *RouteReconciler) Reconcile(ctx context.Context, c hydraclient.Container) (string, error) {
	name := KsvcNameFor(c.ID)
	routes := r.routes.RouteV1().Routes(c.NamespaceK8sName)

	if !c.NeedsOpenShiftRoute {
		if err := routes.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("reconciler: delete route %s/%s: %w", c.NamespaceK8sName, name, err)
		}
		return "", nil
	}

	desired := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.NamespaceK8sName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hydra-operator",
				"hydra.io/agent-id":            c.ID,
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{Kind: "Service", Name: name},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
		},
	}

	existing, err := routes.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created, err := routes.Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("reconciler: create route %s/%s: %w", c.NamespaceK8sName, name, err)
		}
		return endpointFromRoute(created), nil
	}
	if err != nil {
		return "", fmt.Errorf("reconciler: get route %s/%s: %w", c.NamespaceK8sName, name, err)
	}

	desired.ResourceVersion = existing.ResourceVersion
	// Status is a separate subresource: a real API server ignores it on a
	// plain Update, but preserve it explicitly here too since not every fake
	// clientset used in tests enforces that separation.
	desired.Status = existing.Status
	// OpenShift's admission may similarly stamp router/annotator metadata
	// on Create; carry existing top-level annotations forward on Update.
	desired.ObjectMeta.Annotations = existing.ObjectMeta.Annotations
	updated, err := routes.Update(ctx, desired, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("reconciler: update route %s/%s: %w", c.NamespaceK8sName, name, err)
	}
	return endpointFromRoute(updated), nil
}

// endpointFromRoute extracts the endpoint_url reported to Hydra
// (HYDRA-052 AC: "endpoint_url extrait depuis Route.Status.Ingress[0].Host").
func endpointFromRoute(route *routev1.Route) string {
	if len(route.Status.Ingress) == 0 || route.Status.Ingress[0].Host == "" {
		return ""
	}
	return "https://" + route.Status.Ingress[0].Host
}
