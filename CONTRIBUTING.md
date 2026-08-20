# Contributing to hydra-operator

Thanks for your interest in contributing. This document covers everything
you need to get set up, understand how work is planned and tracked, and get
a pull request merged.

By contributing, you agree that your contributions are licensed under the
project's [Apache License 2.0](LICENSE).

## Code of conduct

Be respectful and constructive. Disagree on technical points, not on people.
Maintainers may close issues/PRs or block contributors who don't.

## Getting set up

**Prerequisites**: Go 1.26+, `kubebuilder`'s `envtest` binaries (for
`make test-envtest`), `kind` + `helm` (for the local e2e suite), Docker
(for `make docker`).

```bash
git clone https://github.com/ffurlanetto/hydra-operator.git
cd hydra-operator
go build ./cmd/operator
```

**Before opening a PR**, all of these must pass locally:

```bash
make test      # go test -race ./...
make lint      # go vet
make build     # local binary
```

If your change touches the reconciler, RBAC, or the deploy manifests, also
run:

```bash
make test-envtest       # real kube-apiserver + etcd, no real Knative
make rbac-drift-check   # Kustomize and Helm ClusterRole must match
make deploy-validate    # kubectl kustomize dry-render
make helm-lint
```

See [`README.md`](README.md) for the full command reference, and
[`docs/testing/e2e.md`](docs/testing/e2e.md) for how the three test levels
(unit / envtest / kind e2e) fit together and how to run the full `kind`
suite (`make e2e-local`) if your change needs it.

## How work is planned and tracked

**This repository has no separate ticket tracker.** hydra-operator and
[`hydra`](https://github.com/ffurlanetto/hydra) share one ticket prefix
(`HYDRA-NNN`) and one plan document —
[`hydra/docs/specs/implementation-plan.md`](https://github.com/ffurlanetto/hydra/blob/main/docs/specs/implementation-plan.md)
— because most features span both repos (an API change in `hydra` usually
needs a matching reconciler change here). Active tickets are mirrored as
[GitHub Issues in the `hydra` repo](https://github.com/ffurlanetto/hydra/issues);
check there before starting work, even for a change that only touches this
repository, to avoid duplicate effort and to see the full context
(dependencies, acceptance criteria, related ADRs).

**For anything architectural**: this repo's design decisions are recorded
as ADRs in `hydra/docs/adr/` (e.g. [ADR-024](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-024-operator-model.md)
for the operator-pull model, [ADR-025](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-025-knative-only-control-plane.md)
for Knative-only). A change that would need a new ADR (new dependency, new
reconciliation pattern, a decision that's expensive to reverse) should get
one discussed and accepted there first — same process as `hydra` itself.

## Making changes

**Branch naming**: `type/short-description` (e.g. `fix/heartbeat-retry-backoff`).

**Commit messages**: [Conventional Commits](https://www.conventionalcommits.org/).

```
type(scope): short description

Types: feat | fix | docs | test | refactor | chore | security | perf
```

**Code standards**:
- Business logic stays out of `cmd/operator/main.go` — see the
  `internal/reconciler`, `internal/hydraclient`, `internal/capabilities`
  split already in use.
- Errors are always wrapped with context (`fmt.Errorf("context: %w", err)`),
  never silently dropped.
- Structured logging only, no bare `fmt.Println`.
- No hardcoded secrets; the operator never stores a kubeconfig (ADR-024) —
  don't introduce a code path that would.

## Tests

- **Zero regressions**: the full existing suite must pass before any PR is
  merged.
- New reconciler behavior needs `envtest` coverage, not just unit tests
  with fakes, whenever it depends on real apiserver semantics (admission,
  status subresources, owner references).
- A change to the Knative/Kourier/Gatekeeper interaction should be verified
  against the `kind` e2e suite (`make e2e-local`) when feasible — see
  [`docs/testing/e2e.md`](docs/testing/e2e.md) for known sandbox
  limitations (some environments can't run nested `kind`/`K3s` reliably;
  document a negative result rather than silently skip it).
- Tests must be deterministic — no unmocked `time.Now()`, no unseeded
  randomness.

## Opening a pull request

1. Push your branch and open a PR against `main` as soon as you start —
   draft is fine, and expected, while work is in progress.
2. Fill in the PR template (test plan included).
3. Make sure CI is green before merge. A red job needs a real diagnosis, not
   a shrug — if you believe a failure is unrelated to your change, say so
   explicitly in the PR and re-run it.
4. Mark the PR ready for review once it's actually done — tests, lint,
   build all green, and RBAC/Helm drift checks passing if relevant.
5. PRs are squash-merged.

## Questions

Open a [GitHub Issue](https://github.com/ffurlanetto/hydra-operator/issues)
here for anything specific to this repo (the operator binary, reconciler,
deploy manifests), or an issue in
[`hydra`](https://github.com/ffurlanetto/hydra/issues) for anything about
the control plane API or the overall roadmap.
