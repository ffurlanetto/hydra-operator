package hydraclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
)

func TestClient_Register_ReturnsClusterToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/operator/clusters/cluster-1/register", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "reg-tok", body["registration_token"])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"cluster_token": "jwt-abc"})
	}))
	defer srv.Close()

	c := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	token, err := c.Register(t.Context(), "reg-tok")
	require.NoError(t, err)
	assert.Equal(t, "jwt-abc", token)
	assert.Equal(t, "jwt-abc", c.Token())
}

func TestClient_Heartbeat_SendsCapabilitiesAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/operator/clusters/cluster-1/heartbeat", r.URL.Path)
		require.Equal(t, "Bearer jwt-abc", r.Header.Get("Authorization"))
		var body hydraclient.HeartbeatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, 3, body.NodeCount)
		require.Contains(t, string(body.Capabilities), "openshift_available")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	c.SetToken("jwt-abc")
	err := c.Heartbeat(t.Context(), hydraclient.HeartbeatRequest{
		OperatorVersion: "dev",
		K8sVersion:      "v1.31.0",
		NodeCount:       3,
		Capabilities:    json.RawMessage(`{"openshift_available":true}`),
	})
	require.NoError(t, err)
}

func TestClient_GetDesiredState_DecodesFullPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/operator/clusters/cluster-1/desired-state", r.URL.Path)
		require.Equal(t, "Bearer jwt-abc", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"revision": "sha256:abc",
			"namespaces": [{"id":"ns-1","k8s_name":"prod","mode":"managed","quotas":{}}],
			"containers": [{
				"id":"agent-1","namespace_k8s_name":"prod",
				"definition":{"image":"img:v1","cpu_limit":"500m","memory_limit":"512Mi"},
				"scaling":{"min_scale":1,"max_scale":3},
				"traffic":[{"revision_name":"agent-1-00001","percent":100}],
				"restart_generation":2,
				"runtime_class_name":"kata-containers",
				"custom_domains":["app.example.com"],
				"needs_openshift_route":true
			}],
			"security_policy":{"allowed_registries":["registry.example.com"]}
		}`))
	}))
	defer srv.Close()

	c := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	c.SetToken("jwt-abc")
	state, err := c.GetDesiredState(t.Context())
	require.NoError(t, err)

	require.Len(t, state.Namespaces, 1)
	assert.Equal(t, "prod", state.Namespaces[0].K8sName)
	require.Len(t, state.Containers, 1)
	container := state.Containers[0]
	assert.Equal(t, "img:v1", container.Definition.Image)
	assert.Equal(t, 2, container.RestartGeneration)
	assert.True(t, container.NeedsOpenShiftRoute)
	assert.Equal(t, []string{"app.example.com"}, container.CustomDomains)
	assert.Equal(t, []string{"registry.example.com"}, state.SecurityPolicy.AllowedRegistries)
}

func TestClient_PutAgentK8sStatus_SendsExpectedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/operator/agents/agent-1/k8s-status", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)
		var body hydraclient.AgentK8sStatusRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "running", body.Status)
		require.Equal(t, "https://agent.example.com", body.EndpointURL)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	c.SetToken("jwt-abc")
	err := c.PutAgentK8sStatus(t.Context(), "agent-1", hydraclient.AgentK8sStatusRequest{
		Status:        "running",
		ReadyReplicas: 1,
		EndpointURL:   "https://agent.example.com",
	})
	require.NoError(t, err)
}

func TestClient_UnexpectedStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"agent does not belong to the authenticated cluster"}`))
	}))
	defer srv.Close()

	c := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	c.SetToken("jwt-abc")
	err := c.PutAgentK8sStatus(t.Context(), "agent-1", hydraclient.AgentK8sStatusRequest{Status: "running"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}
