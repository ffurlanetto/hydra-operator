// Package k8sclient builds the Kubernetes API clients hydra-operator needs:
// the standard client-go clientset (Namespaces, Secrets, ResourceQuotas),
// Knative Serving's typed clientset (Service, Revision, Route,
// DomainMapping), OpenShift's Route clientset (used only when a cluster's
// capabilities require it, HYDRA-052), and a dynamic client for Gatekeeper's
// ConstraintTemplate/Constraint CRDs (ADR-026 MVP), which have no generated
// Go clientset.
package k8sclient

import (
	"fmt"

	routeversioned "github.com/openshift/client-go/route/clientset/versioned"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	knativeversioned "knative.dev/serving/pkg/client/clientset/versioned"
)

// Clients bundles every typed clientset the reconcilers need.
type Clients struct {
	Core    kubernetes.Interface
	Knative knativeversioned.Interface
	Route   routeversioned.Interface
	Dynamic dynamic.Interface
}

// NewInCluster builds Clients using the in-cluster service account config —
// the normal path when hydra-operator runs as a Deployment inside the
// cluster it reconciles.
func NewInCluster() (*Clients, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8sclient: in-cluster config: %w", err)
	}
	return newForConfig(cfg)
}

// NewFromKubeconfig builds Clients from a kubeconfig file path — used for
// local development against a kind/k3d/CRC cluster outside of the cluster
// itself.
func NewFromKubeconfig(path string) (*Clients, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("k8sclient: load kubeconfig %s: %w", path, err)
	}
	return newForConfig(cfg)
}

func newForConfig(cfg *rest.Config) (*Clients, error) {
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sclient: core clientset: %w", err)
	}
	knativeClient, err := knativeversioned.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sclient: knative clientset: %w", err)
	}
	routeClient, err := routeversioned.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sclient: openshift route clientset: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sclient: dynamic client: %w", err)
	}
	return &Clients{Core: core, Knative: knativeClient, Route: routeClient, Dynamic: dynamicClient}, nil
}
