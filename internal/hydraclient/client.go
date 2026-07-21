// Package hydraclient is a typed HTTP client for the Hydra control-plane
// operator-facing API (ADR-024/025): registration, heartbeat, desired-state
// pull, and status callbacks. hydra-operator never connects to Hydra's
// database directly — this is the only channel between the two.
package hydraclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to one Hydra cluster's operator-facing endpoints.
type Client struct {
	baseURL   string
	clusterID string
	http      *http.Client

	// token is the current cluster JWT, injected as a Bearer token on every
	// request except Register (which authenticates with the one-time
	// registration token instead). Set via SetToken after Register or when
	// loaded from the token store.
	token string
}

// New builds a Client for baseURL (Hydra's API root, e.g.
// "https://hydra.example.com") and clusterID, with requests bounded by timeout.
func New(baseURL, clusterID string, timeout time.Duration) *Client {
	return &Client{
		baseURL:   baseURL,
		clusterID: clusterID,
		http:      &http.Client{Timeout: timeout},
	}
}

// SetToken updates the cluster JWT used to authenticate subsequent requests.
func (c *Client) SetToken(token string) {
	c.token = token
}

// Token returns the current cluster JWT ("" if not yet registered).
func (c *Client) Token() string {
	return c.token
}

// Register exchanges a one-time registration token for a long-lived cluster
// JWT. Called once on first startup; subsequent restarts load the token
// from the token store instead (see internal/tokenstore).
func (c *Client) Register(ctx context.Context, registrationToken string) (string, error) {
	var resp struct {
		ClusterToken string `json:"cluster_token"`
	}
	body := map[string]string{"registration_token": registrationToken}
	if err := c.do(ctx, http.MethodPost, "/operator/clusters/"+c.clusterID+"/register", body, false, &resp); err != nil {
		return "", fmt.Errorf("hydraclient: register: %w", err)
	}
	c.token = resp.ClusterToken
	return resp.ClusterToken, nil
}

// RotateToken proactively rotates the cluster JWT before expiry, replacing
// the client's stored token with the new one.
func (c *Client) RotateToken(ctx context.Context) (string, error) {
	var resp struct {
		ClusterToken string `json:"cluster_token"`
	}
	if err := c.do(ctx, http.MethodPost, "/operator/clusters/"+c.clusterID+"/rotate-token", nil, true, &resp); err != nil {
		return "", fmt.Errorf("hydraclient: rotate token: %w", err)
	}
	c.token = resp.ClusterToken
	return resp.ClusterToken, nil
}

// HeartbeatRequest reports operator liveness and detected cluster capabilities.
type HeartbeatRequest struct {
	OperatorVersion string          `json:"operator_version"`
	K8sVersion      string          `json:"k8s_version"`
	NodeCount       int             `json:"node_count"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
}

// Heartbeat posts liveness + capabilities. Called every Reconciler.HeartbeatInterval.
func (c *Client) Heartbeat(ctx context.Context, req HeartbeatRequest) error {
	if err := c.do(ctx, http.MethodPost, "/operator/clusters/"+c.clusterID+"/heartbeat", req, true, nil); err != nil {
		return fmt.Errorf("hydraclient: heartbeat: %w", err)
	}
	return nil
}

// ClusterStatusRequest reports overall cluster reachability and sync counts.
type ClusterStatusRequest struct {
	Status           string `json:"status"` // "reachable" | "unreachable"
	NamespacesSynced int    `json:"namespaces_synced,omitempty"`
	ContainersReady  int    `json:"containers_ready,omitempty"`
}

// PutClusterStatus reports the cluster's overall reachability.
func (c *Client) PutClusterStatus(ctx context.Context, req ClusterStatusRequest) error {
	if err := c.do(ctx, http.MethodPut, "/operator/clusters/"+c.clusterID+"/status", req, true, nil); err != nil {
		return fmt.Errorf("hydraclient: put cluster status: %w", err)
	}
	return nil
}

// Quotas mirrors Hydra's desiredStateQuotas.
type Quotas struct {
	LimitsCPU      string `json:"limits_cpu,omitempty"`
	LimitsMemory   string `json:"limits_memory,omitempty"`
	RequestsCPU    string `json:"requests_cpu,omitempty"`
	RequestsMemory string `json:"requests_memory,omitempty"`
	CountPods      int    `json:"count_pods,omitempty"`
}

// Namespace mirrors Hydra's desiredStateNamespace.
type Namespace struct {
	ID      string `json:"id"`
	K8sName string `json:"k8s_name"`
	Mode    string `json:"mode"`
	Quotas  Quotas `json:"quotas"`
}

// EnvVar mirrors Hydra's domain.EnvVar.
type EnvVar struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
}

// PortSpec mirrors Hydra's domain.PortSpec.
type PortSpec struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int32  `json:"container_port"`
	Protocol      string `json:"protocol,omitempty"`
}

// Definition mirrors Hydra's desiredStateDefinition.
type Definition struct {
	Image           string     `json:"image"`
	CPULimit        string     `json:"cpu_limit"`
	MemoryLimit     string     `json:"memory_limit"`
	Env             []EnvVar   `json:"env,omitempty"`
	Ports           []PortSpec `json:"ports,omitempty"`
	HealthCheckPath string     `json:"health_check_path,omitempty"`
}

// Scaling mirrors Hydra's desiredStateScaling.
type Scaling struct {
	MinScale          int `json:"min_scale"`
	MaxScale          int `json:"max_scale"`
	ConcurrencyTarget int `json:"concurrency_target,omitempty"`
}

// Traffic mirrors Hydra's desiredStateTraffic.
type Traffic struct {
	RevisionName string `json:"revision_name"`
	Percent      int    `json:"percent"`
}

// CustomDomain mirrors one entry of Hydra's desired-state
// Container.CustomDomains (Epic 15, HYDRA-094).
//
// Expected shape from Hydra's API once the domain-ID follow-up lands:
//
//	{"id": "<custom_domain row UUID>", "hostname": "app.example.com"}
//
// ID is intentionally optional on the wire and may legitimately be absent or
// empty:
//   - an older (or not-yet-updated) Hydra API that only ever sent hostnames
//   - a future Hydra API that, for some other reason, doesn't attach an ID
//     to a given entry
//
// In both cases the operator must NOT crash or error — it simply skips the
// PUT /operator/domains/:domainId/status callback for that entry (see
// Manager.reportDomainStatuses in internal/reconciler/manager.go), since the
// callback has no ID to address. This is a best-effort guess at the field
// name Hydra's independent desired-state change will use; reconcile this
// struct (and its JSON tags) against the actual hydra-side payload before/at
// merge time if it differs.
//
// For backward compatibility with the current Hydra API — which serializes
// custom_domains as a plain []string of bare hostnames — UnmarshalJSON also
// accepts a bare JSON string as shorthand for {"hostname": <string>} with no
// ID, so this client keeps decoding GetDesiredState correctly whether or not
// the hydra-side change has landed yet.
type CustomDomain struct {
	ID       string `json:"id,omitempty"`
	Hostname string `json:"hostname"`
}

// UnmarshalJSON accepts either a bare JSON string (the current Hydra API's
// []string of hostnames) or a {"id":..., "hostname":...} object (the
// expected future shape once Hydra starts sending domain IDs), so decoding
// GetDesiredState never fails regardless of which side of that rollout the
// two services are on.
func (d *CustomDomain) UnmarshalJSON(data []byte) error {
	var hostname string
	if err := json.Unmarshal(data, &hostname); err == nil {
		d.ID = ""
		d.Hostname = hostname
		return nil
	}

	type alias CustomDomain // avoid recursing back into this UnmarshalJSON
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("hydraclient: unmarshal custom domain: %w", err)
	}
	*d = CustomDomain(a)
	return nil
}

// Container mirrors Hydra's desiredStateContainer (the ServerlessContainer
// facade over AgentDefinition+Agent, HYDRA-079).
type Container struct {
	ID                  string         `json:"id"`
	NamespaceK8sName    string         `json:"namespace_k8s_name"`
	Definition          Definition     `json:"definition"`
	Scaling             Scaling        `json:"scaling"`
	Traffic             []Traffic      `json:"traffic"`
	RestartGeneration   int            `json:"restart_generation"`
	RuntimeClassName    string         `json:"runtime_class_name"`
	CustomDomains       []CustomDomain `json:"custom_domains,omitempty"`
	NeedsOpenShiftRoute bool           `json:"needs_openshift_route,omitempty"`
}

// SecurityPolicy mirrors Hydra's desiredStateSecurityPolicy.
type SecurityPolicy struct {
	AllowedRegistries []string `json:"allowed_registries,omitempty"`
	AllowedSigners    []string `json:"allowed_signers,omitempty"`
}

// DesiredState mirrors Hydra's desiredStateResponse.
type DesiredState struct {
	Revision       string         `json:"revision"`
	Namespaces     []Namespace    `json:"namespaces"`
	Containers     []Container    `json:"containers"`
	SecurityPolicy SecurityPolicy `json:"security_policy"`
}

// GetDesiredState pulls the full desired state for this cluster. Called
// every Reconciler.SyncInterval.
func (c *Client) GetDesiredState(ctx context.Context) (*DesiredState, error) {
	var resp DesiredState
	if err := c.do(ctx, http.MethodGet, "/operator/clusters/"+c.clusterID+"/desired-state", nil, true, &resp); err != nil {
		return nil, fmt.Errorf("hydraclient: get desired state: %w", err)
	}
	return &resp, nil
}

// NamespaceSyncStatusRequest reports whether a namespace's actual K8s state
// matches Hydra's desired state.
type NamespaceSyncStatusRequest struct {
	SyncStatus string `json:"sync_status"` // "synced" | "pending" | "drifted" | "error"
	SyncError  string `json:"sync_error,omitempty"`
}

// PutNamespaceSyncStatus reports namespace reconciliation outcome.
func (c *Client) PutNamespaceSyncStatus(ctx context.Context, namespaceID string, req NamespaceSyncStatusRequest) error {
	if err := c.do(ctx, http.MethodPut, "/operator/namespaces/"+namespaceID+"/sync-status", req, true, nil); err != nil {
		return fmt.Errorf("hydraclient: put namespace sync status: %w", err)
	}
	return nil
}

// RevisionEntry mirrors Hydra's domain.RevisionEntry.
type RevisionEntry struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// AgentK8sStatusRequest reports the real Knative-derived status of a
// container — the only way Hydra learns it (ADR-024/025).
type AgentK8sStatusRequest struct {
	Status         string          `json:"status"` // "deploying" | "running" | "degraded" | "stopped" | "error"
	ReadyReplicas  int             `json:"ready_replicas"`
	EndpointURL    string          `json:"endpoint_url,omitempty"`
	ActiveRevision string          `json:"active_revision,omitempty"`
	Revisions      []RevisionEntry `json:"revisions,omitempty"`
	K8sConditions  json.RawMessage `json:"k8s_conditions,omitempty"`
}

// PutAgentK8sStatus reports a container's reconciliation outcome.
func (c *Client) PutAgentK8sStatus(ctx context.Context, agentID string, req AgentK8sStatusRequest) error {
	if err := c.do(ctx, http.MethodPut, "/operator/agents/"+agentID+"/k8s-status", req, true, nil); err != nil {
		return fmt.Errorf("hydraclient: put agent k8s status: %w", err)
	}
	return nil
}

// DomainStatusRequest reports custom domain provisioning outcome.
type DomainStatusRequest struct {
	Status       string `json:"status"` // "active" | "error"
	ErrorMessage string `json:"error_message,omitempty"`
}

// PutDomainStatus reports a custom domain's Certificate+Route/DomainMapping
// provisioning outcome, the final step of the Epic 15 domain lifecycle.
func (c *Client) PutDomainStatus(ctx context.Context, domainID string, req DomainStatusRequest) error {
	if err := c.do(ctx, http.MethodPut, "/operator/domains/"+domainID+"/status", req, true, nil); err != nil {
		return fmt.Errorf("hydraclient: put domain status: %w", err)
	}
	return nil
}

// do executes an HTTP request against path, marshaling body as JSON (if
// non-nil) and unmarshaling the response into out (if non-nil). authed
// controls whether the current Bearer token is attached — only Register
// omits it (it authenticates via the registration token in its body instead).
func (c *Client) do(ctx context.Context, method, path string, body any, authed bool, out any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d from %s %s: %s", resp.StatusCode, method, path, string(respBody))
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
