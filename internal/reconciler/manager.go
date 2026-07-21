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
	Policy            *PolicyReconciler
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

	m.reconcilePolicy(ctx, state.SecurityPolicy)

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

// reconcilePolicy applies Hydra's SecurityPolicy as Gatekeeper Constraints
// (ADR-026 MVP), but only on clusters where Gatekeeper is actually
// installed — same detect-don't-install pattern already used for
// Kourier/OpenShift. A failed detection or reconcile is logged, not fatal:
// policy enforcement is best-effort infrastructure, not gating container
// reconciliation.
func (m *Manager) reconcilePolicy(ctx context.Context, policy hydraclient.SecurityPolicy) {
	caps, err := m.cfg.Capabilities.Detect(ctx)
	if err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: capabilities detect failed (policy reconcile skipped)")
		return
	}
	if !caps.Capabilities.GatekeeperAvailable {
		return
	}
	if err := m.cfg.Policy.Reconcile(ctx, policy); err != nil {
		m.cfg.Log.Error().Err(err).Msg("manager: policy reconcile failed")
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
		// real, and each hostname's outcome is reported back individually
		// via PUT /operator/domains/:domainId/status — but only once Hydra's
		// desired-state payload actually carries a domain ID alongside the
		// hostname (hydraclient.CustomDomain.ID). On an older Hydra API (or
		// any entry Hydra sends without an ID), reportDomainStatuses skips
		// the callback for that domain rather than erroring: the
		// DomainMapping itself is still reconciled either way.
		m.reportDomainStatuses(ctx, m.cfg.Domains.Reconcile(ctx, c))

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

// reportDomainStatuses reports each domain's reconciliation outcome back to
// Hydra via PUT /operator/domains/:domainId/status. A domain whose ID is
// empty is skipped (logged at debug, not an error): that happens whenever
// Hydra's desired-state payload didn't include a domain ID for that entry —
// either an older Hydra API talking to a newer operator, or the Hydra-side
// change that adds domain IDs to Container.CustomDomains hasn't landed yet.
// The DomainMapping itself was already reconciled by Domains.Reconcile
// regardless; only the status callback is affected.
func (m *Manager) reportDomainStatuses(ctx context.Context, statuses []DomainStatus) {
	for _, s := range statuses {
		if s.ID == "" {
			m.cfg.Log.Debug().Str("hostname", s.Hostname).
				Msg("manager: skipping domain status callback, no domain ID from Hydra's desired-state payload")
			continue
		}
		if err := m.cfg.Hydra.PutDomainStatus(ctx, s.ID, hydraclient.DomainStatusRequest{
			Status:       s.Status,
			ErrorMessage: s.Error,
		}); err != nil {
			m.cfg.Log.Error().Err(err).Str("hostname", s.Hostname).Str("domain_id", s.ID).
				Msg("manager: report domain status failed")
		}
	}
}
