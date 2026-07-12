package reconciler

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	Hydra             *hydraclient.Client
	Namespaces        *NamespaceReconciler
	Containers        *ContainerReconciler
	Routes            *RouteReconciler
	Domains           *DomainReconciler
	Capabilities      *capabilities.Detector
	Log               zerolog.Logger
	OperatorVersion   string
	SyncInterval      time.Duration
	HeartbeatInterval time.Duration
}

// Manager orchestrates the two periodic jobs ADR-024/025's pull-based model
// requires: the sync loop (pull desired-state, reconcile everything, report
// statuses back) and the heartbeat loop (capability detection + liveness).
type Manager struct {
	cfg ManagerConfig
}

// NewManager builds a Manager from cfg.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{cfg: cfg}
}

// Run drives both loops until ctx is canceled. It blocks.
func (m *Manager) Run(ctx context.Context) {
	go m.runLoop(ctx, m.cfg.HeartbeatInterval, m.HeartbeatOnce)
	m.runLoop(ctx, m.cfg.SyncInterval, m.SyncOnce)
}

// runLoop calls tick immediately (so the first attempt doesn't wait a full
// interval) and then every interval until ctx is canceled.
func (m *Manager) runLoop(ctx context.Context, interval time.Duration, tick func(context.Context)) {
	tick(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick(ctx)
		}
	}
}

// HeartbeatOnce detects capabilities and posts one heartbeat. Exported so
// tests can drive it directly instead of waiting on a real ticker.
func (m *Manager) HeartbeatOnce(ctx context.Context) {
	result, err := m.cfg.Capabilities.Detect(ctx)
	if err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: capabilities detect failed")
		return
	}
	capsJSON, err := result.MarshalCapabilitiesJSON()
	if err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: marshal capabilities failed")
		return
	}
	if err := m.cfg.Hydra.Heartbeat(ctx, hydraclient.HeartbeatRequest{
		OperatorVersion: m.cfg.OperatorVersion,
		K8sVersion:      result.K8sVersion,
		NodeCount:       result.NodeCount,
		Capabilities:    capsJSON,
	}); err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: heartbeat failed")
	}
}

// SyncOnce pulls desired state, reconciles every namespace and container,
// and reports statuses back. Exported so tests can drive it directly
// instead of waiting on a real ticker.
func (m *Manager) SyncOnce(ctx context.Context) {
	state, err := m.cfg.Hydra.GetDesiredState(ctx)
	if err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: get desired state failed")
		if statusErr := m.cfg.Hydra.PutClusterStatus(ctx, hydraclient.ClusterStatusRequest{Status: "unreachable"}); statusErr != nil {
			m.cfg.Log.Error().Err(statusErr).Msg("manager: report cluster unreachable failed")
		}
		return
	}

	namespacesSynced := m.reconcileNamespaces(ctx, state.Namespaces)
	containersReady := m.reconcileContainers(ctx, state.Containers)

	if err := m.cfg.Hydra.PutClusterStatus(ctx, hydraclient.ClusterStatusRequest{
		Status:           "reachable",
		NamespacesSynced: namespacesSynced,
		ContainersReady:  containersReady,
	}); err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: report cluster status failed")
	}
}

func (m *Manager) reconcileNamespaces(ctx context.Context, namespaces []hydraclient.Namespace) int {
	synced := 0
	for _, ns := range namespaces {
		req := hydraclient.NamespaceSyncStatusRequest{SyncStatus: "synced"}
		if err := m.cfg.Namespaces.Reconcile(ctx, ns); err != nil {
			m.cfg.Log.Error().Err(err).Str("namespace", ns.K8sName).Msg("manager: namespace reconcile failed")
			req = hydraclient.NamespaceSyncStatusRequest{SyncStatus: "error", SyncError: err.Error()}
		} else {
			synced++
		}
		if err := m.cfg.Hydra.PutNamespaceSyncStatus(ctx, ns.ID, req); err != nil {
			m.cfg.Log.Error().Err(err).Str("namespace", ns.K8sName).Msg("manager: report namespace sync status failed")
		}
	}
	return synced
}

func (m *Manager) reconcileContainers(ctx context.Context, containers []hydraclient.Container) int {
	ready := 0
	for _, c := range containers {
		status, err := m.cfg.Containers.Reconcile(ctx, c)
		if err != nil {
			m.cfg.Log.Error().Err(err).Str("container", c.ID).Msg("manager: container reconcile failed")
			if statusErr := m.cfg.Hydra.PutAgentK8sStatus(ctx, c.ID, hydraclient.AgentK8sStatusRequest{Status: "error"}); statusErr != nil {
				m.cfg.Log.Error().Err(statusErr).Str("container", c.ID).Msg("manager: report container error status failed")
			}
			continue
		}

		endpoint := status.EndpointURL
		if routeEndpoint, err := m.cfg.Routes.Reconcile(ctx, c); err != nil {
			m.cfg.Log.Error().Err(err).Str("container", c.ID).Msg("manager: route reconcile failed")
		} else if routeEndpoint != "" {
			endpoint = routeEndpoint
		}

		// Custom domain reconciliation (DomainMapping objects) runs for
		// real, but its per-hostname outcome can't be reported back via
		// PUT /operator/domains/:domainId/status yet: Hydra's desired-state
		// payload (Container.CustomDomains) carries only hostnames, not the
		// CustomDomain row's UUID that endpoint requires. Needs a follow-up
		// on the Hydra side to include domain IDs alongside hostnames.
		m.cfg.Domains.Reconcile(ctx, c)

		if status.Status == "running" {
			ready++
		}

		if err := m.cfg.Hydra.PutAgentK8sStatus(ctx, c.ID, hydraclient.AgentK8sStatusRequest{
			Status:         status.Status,
			ReadyReplicas:  status.ReadyReplicas,
			EndpointURL:    endpoint,
			ActiveRevision: status.ActiveRevision,
			Revisions:      status.Revisions,
		}); err != nil {
			m.cfg.Log.Error().Err(err).Str("container", c.ID).Msg("manager: report container status failed")
		}
	}
	return ready
}
