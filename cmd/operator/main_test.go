package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeversion "k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
)

func newFakeClientWithGroups(groupVersions ...string) *fake.Clientset {
	client := fake.NewClientset()
	fakeDiscovery := client.Discovery().(*discoveryfake.FakeDiscovery)
	fakeDiscovery.FakedServerVersion = &kubeversion.Info{GitVersion: "v1.31.0"}

	resources := make([]*metav1.APIResourceList, 0, len(groupVersions))
	for _, gv := range groupVersions {
		resources = append(resources, &metav1.APIResourceList{GroupVersion: gv})
	}
	fakeDiscovery.Resources = resources
	return client
}

func TestCheckKnativeAvailable_KnativePresent_ReturnsNil(t *testing.T) {
	client := newFakeClientWithGroups("serving.knative.dev/v1")
	detector := capabilities.New(client, time.Second)

	err := checkKnativeAvailable(context.Background(), detector)
	assert.NoError(t, err)
}

func TestCheckKnativeAvailable_KnativeAbsent_ReturnsErrKnativeNotAvailable(t *testing.T) {
	client := newFakeClientWithGroups("route.openshift.io/v1")
	detector := capabilities.New(client, time.Second)

	err := checkKnativeAvailable(context.Background(), detector)
	require.Error(t, err)
	assert.ErrorIs(t, err, errKnativeNotAvailable)
}

func TestHealthMux_ReadyzReflectsReadyFlag(t *testing.T) {
	var ready atomic.Bool
	srv := httptest.NewServer(newHealthMux(&ready))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	resp.Body.Close()

	ready.Store(true)

	resp, err = http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestHealthMux_HealthzAlwaysOK(t *testing.T) {
	var ready atomic.Bool
	srv := httptest.NewServer(newHealthMux(&ready))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
