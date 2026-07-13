package reconciler

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// constraintTemplateGVR is Gatekeeper's own CRD for defining new Constraint
// kinds — no generated Go clientset exists for it, hence the dynamic client.
var constraintTemplateGVR = schema.GroupVersionResource{
	Group: "templates.gatekeeper.sh", Version: "v1", Resource: "constrainttemplates",
}

const (
	requiredResourceLimitsKind = "K8sRequiredResourceLimits"
	blockPrivilegedKind        = "K8sBlockPrivileged"
	allowedReposKind           = "K8sAllowedRepos"

	requiredResourceLimitsName = "hydra-required-resource-limits"
	blockPrivilegedName        = "hydra-block-privileged"
	allowedReposName           = "hydra-allowed-repos"

	requiredResourceLimitsRego = `package k8srequiredresourcelimits

violation[{"msg": msg}] {
	container := input.review.object.spec.containers[_]
	not container.resources.limits.cpu
	msg := sprintf("container <%v> has no cpu limit", [container.name])
}

violation[{"msg": msg}] {
	container := input.review.object.spec.containers[_]
	not container.resources.limits.memory
	msg := sprintf("container <%v> has no memory limit", [container.name])
}
`

	blockPrivilegedRego = `package k8sblockprivileged

violation[{"msg": msg}] {
	container := input.review.object.spec.containers[_]
	container.securityContext.privileged
	msg := sprintf("container <%v> must not set privileged: true", [container.name])
}

violation[{"msg": msg}] {
	container := input.review.object.spec.containers[_]
	container.securityContext.allowPrivilegeEscalation
	msg := sprintf("container <%v> must not set allowPrivilegeEscalation: true", [container.name])
}
`

	allowedReposRego = `package k8sallowedrepos

violation[{"msg": msg}] {
	container := input.review.object.spec.containers[_]
	satisfied := [good | repo = input.parameters.repos[_]; good = startswith(container.image, repo)]
	not any(satisfied)
	msg := sprintf("container <%v> has image <%v> from a registry not in the allowed list %v", [container.name, container.image, input.parameters.repos])
}
`
)

// PolicyReconciler applies OPA/Gatekeeper ConstraintTemplates and Constraints
// enforcing Hydra's per-org SecurityPolicy (ADR-026 MVP): mandatory
// cpu/memory resource limits, no privileged/allowPrivilegeEscalation
// containers, and a registry allowlist. Gatekeeper itself is expected to
// already be installed on the cluster by the platform team — this
// reconciler only manages its own ConstraintTemplates/Constraints, the same
// division of responsibility already used for Kourier/OpenShift (detected,
// never installed by this operator). cosign/sigstore image-signature
// verification — the other half of ADR-026's decision — is a documented
// follow-up, not implemented here.
type PolicyReconciler struct {
	dynamic dynamic.Interface
}

// NewPolicyReconciler builds a PolicyReconciler using dyn.
func NewPolicyReconciler(dyn dynamic.Interface) *PolicyReconciler {
	return &PolicyReconciler{dynamic: dyn}
}

// Reconcile ensures the three ConstraintTemplates and their Constraints
// exist and are up to date. The registry-allowlist Constraint is driven by
// policy.AllowedRegistries; when that list is empty, the Constraint is
// removed rather than applied with an empty allowlist — an org that hasn't
// configured one shouldn't have every image rejected by default.
func (r *PolicyReconciler) Reconcile(ctx context.Context, policy hydraclient.SecurityPolicy) error {
	templates := []struct {
		name string
		kind string
		rego string
	}{
		{requiredResourceLimitsName, requiredResourceLimitsKind, requiredResourceLimitsRego},
		{blockPrivilegedName, blockPrivilegedKind, blockPrivilegedRego},
		{allowedReposName, allowedReposKind, allowedReposRego},
	}
	for _, tpl := range templates {
		if err := r.applyConstraintTemplate(ctx, tpl.name, tpl.kind, tpl.rego); err != nil {
			return fmt.Errorf("reconciler: constraint template %s: %w", tpl.name, err)
		}
	}

	if err := r.applyConstraint(ctx, requiredResourceLimitsKind, requiredResourceLimitsName, map[string]interface{}{}); err != nil {
		return fmt.Errorf("reconciler: constraint %s: %w", requiredResourceLimitsName, err)
	}
	if err := r.applyConstraint(ctx, blockPrivilegedKind, blockPrivilegedName, map[string]interface{}{}); err != nil {
		return fmt.Errorf("reconciler: constraint %s: %w", blockPrivilegedName, err)
	}

	var repoParams map[string]interface{}
	if len(policy.AllowedRegistries) > 0 {
		repos := make([]interface{}, len(policy.AllowedRegistries))
		for i, reg := range policy.AllowedRegistries {
			repos[i] = reg
		}
		repoParams = map[string]interface{}{"repos": repos}
	}
	if err := r.applyConstraint(ctx, allowedReposKind, allowedReposName, repoParams); err != nil {
		return fmt.Errorf("reconciler: constraint %s: %w", allowedReposName, err)
	}
	return nil
}

func (r *PolicyReconciler) applyConstraintTemplate(ctx context.Context, name, kind, rego string) error {
	desired := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "templates.gatekeeper.sh/v1",
		"kind":       "ConstraintTemplate",
		"metadata":   map[string]interface{}{"name": name},
		"spec": map[string]interface{}{
			"crd": map[string]interface{}{
				"spec": map[string]interface{}{
					"names": map[string]interface{}{"kind": kind},
				},
			},
			"targets": []interface{}{
				map[string]interface{}{
					"target": "admission.k8s.gatekeeper.sh",
					"rego":   rego,
				},
			},
		},
	}}
	return applyUnstructured(ctx, r.dynamic.Resource(constraintTemplateGVR), name, desired)
}

// applyConstraint ensures (or removes, if parameters is nil) the named
// Constraint of the given kind. Every Constraint matches Pods cluster-wide:
// Knative's own Deployment/Pod machinery creates the Pods Gatekeeper's
// admission webhook actually reviews, regardless of which namespace a
// tenant's workload lands in.
func (r *PolicyReconciler) applyConstraint(ctx context.Context, kind, name string, parameters map[string]interface{}) error {
	client := r.dynamic.Resource(constraintGVR(kind))
	if parameters == nil {
		if err := client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	desired := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "constraints.gatekeeper.sh/v1beta1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name},
		"spec": map[string]interface{}{
			"match": map[string]interface{}{
				"kinds": []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"kinds":     []interface{}{"Pod"},
					},
				},
			},
			"parameters": parameters,
		},
	}}
	return applyUnstructured(ctx, client, name, desired)
}

// constraintGVR derives a Constraint's resource from its kind — Gatekeeper
// generates one CRD per ConstraintTemplate, with the plural resource name
// set to the lowercased kind (e.g. "K8sAllowedRepos" -> "k8sallowedrepos").
func constraintGVR(kind string) schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: strings.ToLower(kind),
	}
}

// applyUnstructured is the Get-or-Create-or-Update pattern every reconciler
// in this package uses, generalized over the dynamic client since Gatekeeper
// has no typed clientset.
func applyUnstructured(ctx context.Context, client dynamic.ResourceInterface, name string, desired *unstructured.Unstructured) error {
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := client.Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Update(ctx, desired, metav1.UpdateOptions{})
	return err
}
