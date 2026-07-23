// Package capabilities detects what a cluster actually supports —
// OpenShift Routes, Kourier, the Gateway API, Knative Serving itself — so
// hydra-operator can adapt its reconciliation strategy and report the
// findings to Hydra via heartbeat (HYDRA-027, HYDRA-052).
package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	openShiftRouteGroupVersion = "route.openshift.io/v1"
	gatewayAPIGroupVersion     = "gateway.networking.k8s.io/v1"
	knativeServingGroupVersion = "serving.knative.dev/v1"
	gatekeeperGroupVersion     = "constraints.gatekeeper.sh/v1beta1"
	kourierNamespace           = "kourier-system"
	kourierServiceName         = "kourier"
)

// Capabilities is the subset of cluster facts Hydra interprets — mirrors
// domain.ClusterCapabilities on the Hydra side (internal/domain/cluster.go).
type Capabilities struct {
	OpenShiftAvailable bool `json:"openshift_available"`
	KourierPresent     bool `json:"kourier_present"`
	GatewayAPIPresent  bool `json:"gateway_api_present"`
	KnativeAvailable   bool `json:"knative_available"`
	// GatekeeperAvailable gates PolicyReconciler (ADR-026 MVP): OPA/Gatekeeper
	// itself must be installed by the platform team, same as Kourier/OpenShift
	// — this operator only detects it and reconciles Constraints against it.
	GatekeeperAvailable bool `json:"gatekeeper_available"`
}

// Result bundles Capabilities with the other facts reported at heartbeat time.
type Result struct {
	Capabilities Capabilities
	K8sVersion   string
	NodeCount    int
}

// MarshalJSON encodes Capabilities for hydraclient.HeartbeatRequest.Capabilities.
func (r Result) MarshalCapabilitiesJSON() (json.RawMessage, error) {
	data, err := json.Marshal(r.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("capabilities: marshal: %w", err)
	}
	return data, nil
}

// Detector probes a cluster's capabilities via the Kubernetes discovery API.
type Detector struct {
	core         kubernetes.Interface
	probeTimeout time.Duration
}

// New builds a Detector using core's discovery client, bounding each probe
// to probeTimeout.
func New(core kubernetes.Interface, probeTimeout time.Duration) *Detector {
	return &Detector{core: core, probeTimeout: probeTimeout}
}

// Detect probes every capability, the K8s server version, and node count.
// A probe failure for one capability doesn't fail the others — an absent
// API group is expected and reported as false, not an error.
func (d *Detector) Detect(ctx context.Context) (Result, error) {
	var result Result

	result.Capabilities.OpenShiftAvailable = d.groupVersionAvailable(openShiftRouteGroupVersion)
	result.Capabilities.GatewayAPIPresent = d.groupVersionAvailable(gatewayAPIGroupVersion)
	result.Capabilities.KnativeAvailable = d.groupVersionAvailable(knativeServingGroupVersion)
	result.Capabilities.GatekeeperAvailable = d.groupVersionAvailable(gatekeeperGroupVersion)
	result.Capabilities.KourierPresent = d.kourierPresent(ctx)

	version, err := d.core.Discovery().ServerVersion()
	if err != nil {
		return result, fmt.Errorf("capabilities: server version: %w", err)
	}
	result.K8sVersion = version.GitVersion

	nodes, err := d.core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, fmt.Errorf("capabilities: list nodes: %w", err)
	}
	result.NodeCount = len(nodes.Items)

	return result, nil
}

// groupVersionAvailable reports whether the API server serves groupVersion
// (e.g. "route.openshift.io/v1") — absence is a normal, expected outcome on
// most clusters, not an error.
func (d *Detector) groupVersionAvailable(groupVersion string) bool {
	_, err := d.core.Discovery().ServerResourcesForGroupVersion(groupVersion)
	return err == nil
}

// kourierPresent checks for the net-kourier Service in kourier-system, the
// standard install location for Knative's Kourier ingress.
func (d *Detector) kourierPresent(ctx context.Context) bool {
	_, err := d.core.CoreV1().Services(kourierNamespace).Get(ctx, kourierServiceName, metav1.GetOptions{})
	return err == nil
}
