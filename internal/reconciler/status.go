package reconciler

import (
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// ContainerStatus is the best-effort snapshot of a Knative Service's
// reconciliation outcome, mapped onto hydraclient.AgentK8sStatusRequest's
// vocabulary ("deploying" | "running" | "degraded" | "error"). It is a
// snapshot, not a final answer — Knative's own controller reconciles
// asynchronously, so a freshly-applied Service is normally still
// "deploying" until a later sync_interval tick observes it "running".
type ContainerStatus struct {
	Status         string
	ReadyReplicas  int
	EndpointURL    string
	ActiveRevision string
	Revisions      []hydraclient.RevisionEntry
}

// deriveContainerStatus reads a Knative Service's .Status to build the
// snapshot reported back to Hydra via PUT /operator/agents/:id/k8s-status.
func deriveContainerStatus(svc *servingv1.Service) ContainerStatus {
	out := ContainerStatus{Status: "deploying"}

	if svc.Status.URL != nil {
		out.EndpointURL = svc.Status.URL.String()
	}
	out.ActiveRevision = svc.Status.LatestReadyRevisionName

	if cond := svc.Status.GetCondition(servingv1.ServiceConditionReady); cond != nil {
		switch cond.Status {
		case "True":
			out.Status = "running"
			out.ReadyReplicas = 1
		case "False":
			out.Status = "degraded"
		default:
			out.Status = "deploying"
		}
	}

	seen := map[string]bool{}
	for _, t := range svc.Status.Traffic {
		if t.RevisionName == "" || seen[t.RevisionName] {
			continue
		}
		seen[t.RevisionName] = true
		out.Revisions = append(out.Revisions, hydraclient.RevisionEntry{Name: t.RevisionName})
	}

	return out
}
