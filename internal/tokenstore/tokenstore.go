// Package tokenstore persists the cluster JWT hydra-operator receives from
// Hydra's one-time registration flow, so a pod restart doesn't require
// re-registering (the registration token is single-use and expires).
package tokenstore

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// tokenKey is the Secret data key holding the cluster JWT.
const tokenKey = "cluster-token"

// Store persists the cluster JWT in a single Kubernetes Secret.
type Store struct {
	client     kubernetes.Interface
	namespace  string
	secretName string
}

// New builds a Store backed by the Secret named secretName in namespace
// (typically the operator's own namespace).
func New(client kubernetes.Interface, namespace, secretName string) *Store {
	return &Store{client: client, namespace: namespace, secretName: secretName}
}

// Load returns the persisted cluster JWT, or "" if the Secret doesn't exist
// yet (the operator hasn't completed registration).
func (s *Store) Load(ctx context.Context) (string, error) {
	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("tokenstore: load: %w", err)
	}
	return string(secret.Data[tokenKey]), nil
}

// Save creates or updates the Secret with token, so it survives a restart.
func (s *Store) Save(ctx context.Context, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.secretName,
			Namespace: s.namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			tokenKey: []byte(token),
		},
	}

	_, err := s.client.CoreV1().Secrets(s.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("tokenstore: save: %w", err)
	}
	return nil
}
