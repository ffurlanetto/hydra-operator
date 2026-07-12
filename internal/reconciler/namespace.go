package reconciler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// resourceQuotaName is the single ResourceQuota hydra-operator manages per
// namespace, sourced from Hydra's per-namespace quota configuration.
const resourceQuotaName = "hydra-quota"

// NamespaceReconciler ensures a Kubernetes Namespace and its ResourceQuota
// match Hydra's desired state for it.
type NamespaceReconciler struct {
	core kubernetes.Interface
}

// NewNamespaceReconciler builds a NamespaceReconciler using core.
func NewNamespaceReconciler(core kubernetes.Interface) *NamespaceReconciler {
	return &NamespaceReconciler{core: core}
}

// Reconcile ensures ns.K8sName exists — created if ns.Mode is "managed",
// required to already exist if "imported" — and applies (or removes) a
// ResourceQuota matching ns.Quotas.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, ns hydraclient.Namespace) error {
	if ns.Mode == "managed" {
		if err := r.ensureNamespace(ctx, ns.K8sName); err != nil {
			return err
		}
	} else {
		if _, err := r.core.CoreV1().Namespaces().Get(ctx, ns.K8sName, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("reconciler: imported namespace %s: %w", ns.K8sName, err)
		}
	}

	return r.reconcileQuota(ctx, ns)
}

func (r *NamespaceReconciler) ensureNamespace(ctx context.Context, name string) error {
	_, err := r.core.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("reconciler: get namespace %s: %w", name, err)
	}

	_, err = r.core.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "hydra-operator"},
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("reconciler: create namespace %s: %w", name, err)
	}
	return nil
}

func (r *NamespaceReconciler) reconcileQuota(ctx context.Context, ns hydraclient.Namespace) error {
	hasQuota := ns.Quotas.LimitsCPU != "" || ns.Quotas.LimitsMemory != "" ||
		ns.Quotas.RequestsCPU != "" || ns.Quotas.RequestsMemory != "" || ns.Quotas.CountPods > 0

	quotas := r.core.CoreV1().ResourceQuotas(ns.K8sName)
	existing, err := quotas.Get(ctx, resourceQuotaName, metav1.GetOptions{})
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("reconciler: get resourcequota in %s: %w", ns.K8sName, err)
	}

	if !hasQuota {
		if exists {
			if err := quotas.Delete(ctx, resourceQuotaName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("reconciler: delete resourcequota in %s: %w", ns.K8sName, err)
			}
		}
		return nil
	}

	hard := corev1.ResourceList{}
	setQuantity(hard, corev1.ResourceLimitsCPU, ns.Quotas.LimitsCPU)
	setQuantity(hard, corev1.ResourceLimitsMemory, ns.Quotas.LimitsMemory)
	setQuantity(hard, corev1.ResourceRequestsCPU, ns.Quotas.RequestsCPU)
	setQuantity(hard, corev1.ResourceRequestsMemory, ns.Quotas.RequestsMemory)
	if ns.Quotas.CountPods > 0 {
		hard[corev1.ResourcePods] = *resource.NewQuantity(int64(ns.Quotas.CountPods), resource.DecimalSI)
	}

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: resourceQuotaName, Namespace: ns.K8sName},
		Spec:       corev1.ResourceQuotaSpec{Hard: hard},
	}

	if exists {
		quota.ResourceVersion = existing.ResourceVersion
		if _, err := quotas.Update(ctx, quota, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("reconciler: update resourcequota in %s: %w", ns.K8sName, err)
		}
		return nil
	}
	if _, err := quotas.Create(ctx, quota, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("reconciler: create resourcequota in %s: %w", ns.K8sName, err)
	}
	return nil
}

func setQuantity(list corev1.ResourceList, name corev1.ResourceName, value string) {
	if value == "" {
		return
	}
	if q, err := resource.ParseQuantity(value); err == nil {
		list[name] = q
	}
}
