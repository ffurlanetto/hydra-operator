package reconciler

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	servingv1beta1 "knative.dev/serving/pkg/apis/serving/v1beta1"
	knativeversioned "knative.dev/serving/pkg/client/clientset/versioned"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// DomainStatus is the outcome of reconciling one custom domain, mapped onto
// hydraclient.DomainStatusRequest's vocabulary ("active" | "error").
type DomainStatus struct {
	Hostname string
	Status   string
	Error    string
}

// DomainReconciler manages one knative.dev DomainMapping per verified
// custom domain (Epic 15, ADR-029) — Hydra's DNS TXT ownership verification
// happens control-plane side; once verified, hydra-operator maps the
// hostname onto the container's own Knative Service, and Knative (with
// net-certmanager installed) handles TLS provisioning automatically.
type DomainReconciler struct {
	knative knativeversioned.Interface
}

// NewDomainReconciler builds a DomainReconciler using knative.
func NewDomainReconciler(knative knativeversioned.Interface) *DomainReconciler {
	return &DomainReconciler{knative: knative}
}

// Reconcile ensures a DomainMapping exists for each of c.CustomDomains,
// pointing at the container's own Knative Service. A failure on one
// hostname doesn't prevent the others from being attempted — each is
// reported independently via PUT /operator/domains/:id/status.
func (r *DomainReconciler) Reconcile(ctx context.Context, c hydraclient.Container) []DomainStatus {
	ksvcName := KsvcNameFor(c.ID)
	statuses := make([]DomainStatus, 0, len(c.CustomDomains))
	for _, hostname := range c.CustomDomains {
		statuses = append(statuses, r.reconcileOne(ctx, c.NamespaceK8sName, hostname, ksvcName))
	}
	return statuses
}

func (r *DomainReconciler) reconcileOne(ctx context.Context, namespace, hostname, ksvcName string) DomainStatus {
	mappings := r.knative.ServingV1beta1().DomainMappings(namespace)

	desired := &servingv1beta1.DomainMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hostname,
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "hydra-operator"},
		},
		Spec: servingv1beta1.DomainMappingSpec{
			Ref: duckv1.KReference{
				APIVersion: "serving.knative.dev/v1",
				Kind:       "Service",
				Name:       ksvcName,
			},
		},
	}

	existing, err := mappings.Get(ctx, hostname, metav1.GetOptions{})
	var applied *servingv1beta1.DomainMapping
	switch {
	case apierrors.IsNotFound(err):
		applied, err = mappings.Create(ctx, desired, metav1.CreateOptions{})
	case err == nil:
		desired.ResourceVersion = existing.ResourceVersion
		// Status is a separate subresource: a real API server ignores it on
		// a plain Update, but preserve it explicitly here too since not
		// every fake clientset used in tests enforces that separation.
		desired.Status = existing.Status
		applied, err = mappings.Update(ctx, desired, metav1.UpdateOptions{})
	}
	if err != nil {
		return DomainStatus{Hostname: hostname, Status: "error", Error: err.Error()}
	}

	if cond := applied.Status.GetCondition(servingv1beta1.DomainMappingConditionReady); cond != nil && cond.Status == "True" {
		return DomainStatus{Hostname: hostname, Status: "active"}
	}
	// Not yet ready is the normal state right after Create/Update — Knative's
	// controller provisions the Ingress and (if configured) the TLS
	// Certificate asynchronously. The manager re-reports on the next
	// sync_interval tick once it becomes Ready.
	return DomainStatus{Hostname: hostname, Status: "error", Error: "not yet ready"}
}
