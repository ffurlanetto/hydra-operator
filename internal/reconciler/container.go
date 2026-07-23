package reconciler

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
	knativeversioned "knative.dev/serving/pkg/client/clientset/versioned"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// restartGenerationAnnotation forces a new Knative Revision when Hydra bumps
// Agent.RestartGeneration — Hydra never patches the cluster directly
// (ADR-024/025), so this annotation on the RevisionTemplate is the only
// signal a manual restart (with no other Spec change) produces one.
const restartGenerationAnnotation = "hydra.io/restart-generation"

// ContainerReconciler reconciles one hydraclient.Container onto a Knative Service.
type ContainerReconciler struct {
	knative knativeversioned.Interface
}

// NewContainerReconciler builds a ContainerReconciler using knative.
func NewContainerReconciler(knative knativeversioned.Interface) *ContainerReconciler {
	return &ContainerReconciler{knative: knative}
}

// Reconcile creates or updates the Knative Service for c, returning a
// best-effort status snapshot from whatever the API server returns
// immediately — Knative's own controller reconciles asynchronously, so
// "running" may only become true on a later call.
func (r *ContainerReconciler) Reconcile(ctx context.Context, c hydraclient.Container) (ContainerStatus, error) {
	name := KsvcNameFor(c.ID)
	desired := buildService(name, c)

	svcs := r.knative.ServingV1().Services(c.NamespaceK8sName)
	existing, err := svcs.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created, err := svcs.Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return ContainerStatus{}, fmt.Errorf("reconciler: create service %s/%s: %w", c.NamespaceK8sName, name, err)
		}
		return deriveContainerStatus(created), nil
	}
	if err != nil {
		return ContainerStatus{}, fmt.Errorf("reconciler: get service %s/%s: %w", c.NamespaceK8sName, name, err)
	}

	desired.ResourceVersion = existing.ResourceVersion
	// Status is a separate subresource: a real API server ignores it on a
	// plain Update, but preserve it explicitly here too since not every fake
	// clientset used in tests enforces that separation.
	desired.Status = existing.Status
	// Knative's admission webhook stamps metadata.annotations on Create
	// (e.g. serving.knative.dev/creator) and rejects any Update that changes
	// or drops them — buildService never sets top-level annotations itself,
	// so carrying the existing ones forward is always safe.
	desired.ObjectMeta.Annotations = existing.ObjectMeta.Annotations
	updated, err := svcs.Update(ctx, desired, metav1.UpdateOptions{})
	if err != nil {
		return ContainerStatus{}, fmt.Errorf("reconciler: update service %s/%s: %w", c.NamespaceK8sName, name, err)
	}
	return deriveContainerStatus(updated), nil
}

func buildService(name string, c hydraclient.Container) *servingv1.Service {
	container := corev1.Container{
		Image: c.Definition.Image,
		Env:   buildEnv(c.Definition.Env),
		Ports: buildPorts(c.Definition.Ports),
		Resources: corev1.ResourceRequirements{
			Limits:   buildResourceList(c.Definition.CPULimit, c.Definition.MemoryLimit),
			Requests: buildResourceList(c.Definition.CPULimit, c.Definition.MemoryLimit),
		},
	}
	if c.Definition.HealthCheckPath != "" {
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: c.Definition.HealthCheckPath},
			},
		}
	}

	annotations := map[string]string{
		restartGenerationAnnotation: strconv.Itoa(c.RestartGeneration),
	}
	if c.Scaling.MinScale > 0 {
		annotations["autoscaling.knative.dev/min-scale"] = strconv.Itoa(c.Scaling.MinScale)
	}
	if c.Scaling.MaxScale > 0 {
		annotations["autoscaling.knative.dev/max-scale"] = strconv.Itoa(c.Scaling.MaxScale)
	}

	var containerConcurrency *int64
	if c.Scaling.ConcurrencyTarget > 0 {
		v := int64(c.Scaling.ConcurrencyTarget)
		containerConcurrency = &v
	}

	return &servingv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.NamespaceK8sName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hydra-operator",
				"hydra.io/agent-id":            c.ID,
			},
		},
		Spec: servingv1.ServiceSpec{
			ConfigurationSpec: servingv1.ConfigurationSpec{
				Template: servingv1.RevisionTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: annotations,
					},
					Spec: servingv1.RevisionSpec{
						PodSpec: corev1.PodSpec{
							Containers:       []corev1.Container{container},
							RuntimeClassName: runtimeClassNamePtr(c.RuntimeClassName),
						},
						ContainerConcurrency: containerConcurrency,
					},
				},
			},
			RouteSpec: servingv1.RouteSpec{
				Traffic: buildTraffic(c.Traffic),
			},
		},
	}
}

func runtimeClassNamePtr(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

func buildEnv(env []hydraclient.EnvVar) []corev1.EnvVar {
	if len(env) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, len(env))
	for _, e := range env {
		v := corev1.EnvVar{Name: e.Name}
		if e.SecretRef != "" {
			// The Secret key defaults to the env var's own name, but callers
			// may set SecretKey explicitly when the key inside the Secret
			// differs from the env var name.
			key := e.Name
			if e.SecretKey != "" {
				key = e.SecretKey
			}
			v.ValueFrom = &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: e.SecretRef},
					Key:                  key,
				},
			}
		} else {
			v.Value = e.Value
		}
		out = append(out, v)
	}
	return out
}

func buildPorts(ports []hydraclient.PortSpec) []corev1.ContainerPort {
	if len(ports) == 0 {
		return nil
	}
	out := make([]corev1.ContainerPort, 0, len(ports))
	for _, p := range ports {
		cp := corev1.ContainerPort{Name: p.Name, ContainerPort: p.ContainerPort}
		if p.Protocol != "" {
			cp.Protocol = corev1.Protocol(p.Protocol)
		}
		out = append(out, cp)
	}
	return out
}

func buildResourceList(cpuLimit, memoryLimit string) corev1.ResourceList {
	list := corev1.ResourceList{}
	setQuantity(list, corev1.ResourceCPU, cpuLimit)
	setQuantity(list, corev1.ResourceMemory, memoryLimit)
	return list
}

// buildTraffic maps Hydra's traffic split onto Knative's RouteSpec.Traffic.
// Empty means "100% to latest ready Revision" — Knative's own default
// routing behavior, used until any Revision has been reported back
// (HYDRA-027) or an explicit rollback/canary split is requested (HYDRA-034).
func buildTraffic(traffic []hydraclient.Traffic) []servingv1.TrafficTarget {
	if len(traffic) == 0 {
		latest := true
		return []servingv1.TrafficTarget{{LatestRevision: &latest, Percent: percentPtr(100)}}
	}
	out := make([]servingv1.TrafficTarget, 0, len(traffic))
	for _, t := range traffic {
		out = append(out, servingv1.TrafficTarget{RevisionName: t.RevisionName, Percent: percentPtr(t.Percent)})
	}
	return out
}

func percentPtr(p int) *int64 {
	v := int64(p)
	return &v
}
