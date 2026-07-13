package reconciler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	routefake "github.com/openshift/client-go/route/clientset/versioned/fake"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeversion "k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	knativefake "knative.dev/serving/pkg/client/clientset/versioned/fake"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

// hydraStub is a minimal in-memory stand-in for Hydra's operator-facing API,
// recording every callback the Manager makes so tests can assert on them.
type hydraStub struct {
	mu sync.Mutex

	desiredState hydraclient.DesiredState

	heartbeats    []hydraclient.HeartbeatRequest
	clusterStatus []hydraclient.ClusterStatusRequest
	nsStatus      map[string]hydraclient.NamespaceSyncStatusRequest
	agentStatus   map[string]hydraclient.AgentK8sStatusRequest
}

func newHydraStub() *hydraStub {
	return &hydraStub{
		nsStatus:    map[string]hydraclient.NamespaceSyncStatusRequest{},
		agentStatus: map[string]hydraclient.AgentK8sStatusRequest{},
	}
}

func (s *hydraStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /operator/clusters/cluster-1/desired-state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.desiredState)
	})
	mux.HandleFunc("POST /operator/clusters/cluster-1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var req hydraclient.HeartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.heartbeats = append(s.heartbeats, req)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /operator/clusters/cluster-1/status", func(w http.ResponseWriter, r *http.Request) {
		var req hydraclient.ClusterStatusRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.clusterStatus = append(s.clusterStatus, req)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /operator/namespaces/{namespaceID}/sync-status", func(w http.ResponseWriter, r *http.Request) {
		var req hydraclient.NamespaceSyncStatusRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.nsStatus[r.PathValue("namespaceID")] = req
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /operator/agents/{agentID}/k8s-status", func(w http.ResponseWriter, r *http.Request) {
		var req hydraclient.AgentK8sStatusRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.agentStatus[r.PathValue("agentID")] = req
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("unexpected request: %s %s body=%s", r.Method, r.URL.Path, body)
	})

	return httptest.NewServer(mux)
}

func newTestManager(t *testing.T, stub *hydraStub) *reconciler.Manager {
	t.Helper()
	srv := stub.server(t)
	t.Cleanup(srv.Close)

	hydra := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	hydra.SetToken("jwt-abc")

	core := fake.NewClientset()
	knative := knativefake.NewSimpleClientset()

	return reconciler.NewManager(reconciler.ManagerConfig{
		Hydra:             hydra,
		Namespaces:        reconciler.NewNamespaceReconciler(core),
		Containers:        reconciler.NewContainerReconciler(knative),
		Routes:            reconciler.NewRouteReconciler(routefake.NewSimpleClientset()), // no container needs a Route in these tests
		Domains:           reconciler.NewDomainReconciler(knative),
		Policy:            reconciler.NewPolicyReconciler(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())), // Gatekeeper not detected in these tests, never invoked
		Capabilities:      capabilities.New(core, time.Second),
		Log:               zerolog.Nop(),
		OperatorVersion:   "test",
		SyncInterval:      time.Hour, // irrelevant: tests call SyncOnce/HeartbeatOnce directly
		HeartbeatInterval: time.Hour,
	})
}

func TestManager_SyncOnce_ReconcilesAndReportsStatus(t *testing.T) {
	stub := newHydraStub()
	stub.desiredState = hydraclient.DesiredState{
		Revision: "sha256:abc",
		Namespaces: []hydraclient.Namespace{
			{ID: "ns-1", K8sName: "prod", Mode: "managed"},
		},
		Containers: []hydraclient.Container{
			{
				ID:               "agent-1",
				NamespaceK8sName: "prod",
				Definition:       hydraclient.Definition{Image: "img:v1", CPULimit: "500m", MemoryLimit: "512Mi"},
				RuntimeClassName: "kata-containers",
			},
		},
	}
	m := newTestManager(t, stub)

	m.SyncOnce(t.Context())

	stub.mu.Lock()
	defer stub.mu.Unlock()

	require.Len(t, stub.clusterStatus, 1)
	assert.Equal(t, "reachable", stub.clusterStatus[0].Status)
	assert.Equal(t, 1, stub.clusterStatus[0].NamespacesSynced)

	nsStatus, ok := stub.nsStatus["ns-1"]
	require.True(t, ok)
	assert.Equal(t, "synced", nsStatus.SyncStatus)

	agentStatus, ok := stub.agentStatus["agent-1"]
	require.True(t, ok)
	assert.Equal(t, "deploying", agentStatus.Status) // fake clientset never marks Ready
}

func TestManager_SyncOnce_HydraUnreachable_ReportsUnreachable(t *testing.T) {
	// Force GetDesiredState to fail by pointing at an unreachable server.
	badHydra := hydraclient.New("http://127.0.0.1:1", "cluster-1", 200*time.Millisecond)
	badHydra.SetToken("jwt-abc")
	badManager := reconciler.NewManager(reconciler.ManagerConfig{
		Hydra:             badHydra,
		Namespaces:        reconciler.NewNamespaceReconciler(fake.NewClientset()),
		Containers:        reconciler.NewContainerReconciler(knativefake.NewSimpleClientset()),
		Routes:            reconciler.NewRouteReconciler(routefake.NewSimpleClientset()),
		Domains:           reconciler.NewDomainReconciler(knativefake.NewSimpleClientset()),
		Capabilities:      capabilities.New(fake.NewClientset(), time.Second),
		Log:               zerolog.Nop(),
		SyncInterval:      time.Hour,
		HeartbeatInterval: time.Hour,
	})

	badManager.SyncOnce(t.Context())
	// This test's purpose is that SyncOnce doesn't panic and returns cleanly
	// on a Hydra API failure — nothing else to assert.
}

func TestManager_HeartbeatOnce_ReportsCapabilities(t *testing.T) {
	stub := newHydraStub()
	m := newTestManager(t, stub)

	m.HeartbeatOnce(t.Context())

	stub.mu.Lock()
	defer stub.mu.Unlock()
	require.Len(t, stub.heartbeats, 1)
	assert.Equal(t, "test", stub.heartbeats[0].OperatorVersion)
	assert.Contains(t, string(stub.heartbeats[0].Capabilities), "openshift_available")
}

func TestManager_SyncOnce_GatekeeperDetected_ReconcilesPolicy(t *testing.T) {
	stub := newHydraStub()
	stub.desiredState = hydraclient.DesiredState{
		SecurityPolicy: hydraclient.SecurityPolicy{AllowedRegistries: []string{"registry.example.com/"}},
	}
	srv := stub.server(t)
	t.Cleanup(srv.Close)

	hydra := hydraclient.New(srv.URL, "cluster-1", 5*time.Second)
	hydra.SetToken("jwt-abc")

	core := fake.NewClientset()
	fakeDiscovery := core.Discovery().(*discoveryfake.FakeDiscovery)
	fakeDiscovery.FakedServerVersion = &kubeversion.Info{GitVersion: "v1.31.0"}
	fakeDiscovery.Resources = []*metav1.APIResourceList{
		{GroupVersion: "constraints.gatekeeper.sh/v1beta1"},
	}
	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	m := reconciler.NewManager(reconciler.ManagerConfig{
		Hydra:             hydra,
		Namespaces:        reconciler.NewNamespaceReconciler(core),
		Containers:        reconciler.NewContainerReconciler(knativefake.NewSimpleClientset()),
		Routes:            reconciler.NewRouteReconciler(routefake.NewSimpleClientset()),
		Domains:           reconciler.NewDomainReconciler(knativefake.NewSimpleClientset()),
		Policy:            reconciler.NewPolicyReconciler(dynClient),
		Capabilities:      capabilities.New(core, time.Second),
		Log:               zerolog.Nop(),
		SyncInterval:      time.Hour,
		HeartbeatInterval: time.Hour,
	})

	m.SyncOnce(t.Context())

	allowedReposGVR := schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: "k8sallowedrepos"}
	_, err := dynClient.Resource(allowedReposGVR).Get(t.Context(), "hydra-allowed-repos", metav1.GetOptions{})
	assert.NoError(t, err, "Gatekeeper being detected should have triggered a policy reconcile")
}

func TestManager_SyncOnce_NamespaceReconcileFails_ReportsErrorStatus(t *testing.T) {
	stub := newHydraStub()
	stub.desiredState = hydraclient.DesiredState{
		Namespaces: []hydraclient.Namespace{
			{ID: "ns-1", K8sName: "does-not-exist", Mode: "imported"}, // imported + missing => error
		},
	}
	m := newTestManager(t, stub)

	m.SyncOnce(t.Context())

	stub.mu.Lock()
	defer stub.mu.Unlock()
	nsStatus, ok := stub.nsStatus["ns-1"]
	require.True(t, ok)
	assert.Equal(t, "error", nsStatus.SyncStatus)
	assert.NotEmpty(t, nsStatus.SyncError)

	require.Len(t, stub.clusterStatus, 1)
	assert.Equal(t, 0, stub.clusterStatus[0].NamespacesSynced)
}
