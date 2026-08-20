# hydra-operator

In-cluster operator for [Hydra](https://github.com/ffurlanetto/hydra). Pulls desired state from the Hydra control plane API over an outbound HTTPS connection and reconciles it against Kubernetes/Knative — no kubeconfig is ever stored in Hydra (see [ADR-024](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-024-operator-model.md) and [ADR-025](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-025-knative-only-control-plane.md)).

## Status

Implemented: registration + heartbeat against the Hydra API (`internal/hydraclient`, `internal/tokenstore`), capability detection (Knative/Kourier/OpenShift Routes/Gatekeeper — `internal/capabilities`), the reconciler control loop for namespaces, Knative Services/DomainMappings, OpenShift Routes and custom-domain DNS status (`internal/reconciler`), and an ADR-026 MVP policy reconciler that applies Gatekeeper `ConstraintTemplate`s/`Constraint`s when Gatekeeper is present (`internal/reconciler/policy.go`). The operator refuses to start (fails its `/readyz`) if Knative Serving isn't detected on the cluster — see `checkKnativeAvailable` in `cmd/operator/main.go`.

Not yet implemented: cosign/sigstore image-signature verification (ADR-026 Phase 2) and Kata Containers node-pool isolation (ADR-026, out of scope for now) — see `docs/adr/ADR-026-policy-engine-image-signing-isolation.md` in the `hydra` repo for the tracked follow-up.

Tested at three levels — see [`docs/testing/e2e.md`](docs/testing/e2e.md): unit tests (`go test ./...`), `envtest` (real kube-apiserver+etcd, no real Knative), and `kind` e2e (real Knative+Kourier, `make e2e-local`).

## Development

```bash
go build ./cmd/operator
go test -race ./...
go vet ./...
```

## Configuration

See `config.example.yaml`. All settings can be overridden via `HYDRA_*` environment variables (e.g. `HYDRA_URL`, `HYDRA_CLUSTER_ID`, `HYDRA_REGISTRATION_TOKEN`).

## Deployment

See [`deploy/README.md`](deploy/README.md) for the full install procedure, including the hard Knative Serving prerequisite. Two equivalent manifest sets are provided — pick one:

- **Kustomize** (`deploy/base` + `deploy/overlays/example`) — simplest, `kubectl` only.
- **Helm** (`helm/`) — same manifests packaged as a chart, for clusters standardized on Helm delivery (e.g. Rancher's catalog) or as a stepping stone toward an OLM bundle for OpenShift's OperatorHub.

CI renders both and fails the build if their `ClusterRole` RBAC diverges (`make rbac-drift-check`).

## Build

```bash
make build       # local binary
make docker      # multi-arch image (requires buildx)
make deploy-validate  # kubectl kustomize dry-render of deploy/base and the example overlay
make helm-lint    # helm lint ./helm
make helm-template  # render the Helm chart to stdout
```

## License

[Apache License 2.0](LICENSE)
