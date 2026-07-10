# hydra-operator

In-cluster operator for [Hydra](https://github.com/ffurlanetto/hydra). Pulls desired state from the Hydra control plane API over an outbound HTTPS connection and reconciles it against Kubernetes/Knative — no kubeconfig is ever stored in Hydra (see [ADR-024](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-024-operator-model.md) and [ADR-025](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-025-knative-only-control-plane.md)).

## Status

Bootstrap stage: project scaffold only (module, entrypoint, config, Makefile, Dockerfile, CI). Registration, desired-state pull, heartbeat, capability detection, and the Knative reconciler are implemented as part of Hydra's Epic 2 (`docs/specs/implementation-plan.md` in the `hydra` repo).

## Development

```bash
go build ./cmd/operator
go test ./...
go vet ./...
```

## Configuration

See `config.example.yaml`. All settings can be overridden via `HYDRA_*` environment variables (e.g. `HYDRA_URL`, `HYDRA_CLUSTER_ID`, `HYDRA_REGISTRATION_TOKEN`).

## Build

```bash
make build   # local binary
make docker  # multi-arch image (requires buildx)
```

## License

Internal — part of the Hydra platform.
