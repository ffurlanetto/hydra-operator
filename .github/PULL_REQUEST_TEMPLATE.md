## Summary

<!-- What does this PR do, and why? Link the ticket if there is one
     (e.g. HYDRA-123 — tracked as a GitHub issue in the `hydra` repo,
     see CONTRIBUTING.md for why tickets live there). -->

-

## Test plan

<!-- How did you verify this works? Be specific — commands run, scenarios
     exercised. -->

- [ ] `make test` passes (`go test -race ./...`)
- [ ] `make lint` passes (`go vet`)
- [ ] `make build` succeeds
- [ ] `make test-envtest` passes, if this touches the reconciler, RBAC, or CRDs
- [ ] `make rbac-drift-check` / `make deploy-validate` / `make helm-lint` pass, if this touches `deploy/` or `helm/`
- [ ] Verified against `make e2e-local` (kind), if this touches Knative/Kourier/Gatekeeper interaction

## Checklist

- [ ] No secrets, credentials, or kubeconfig committed
- [ ] New/changed logic has test coverage
- [ ] Docs updated if behavior or setup changed (`README.md`, `docs/testing/e2e.md`, an ADR in the `hydra` repo if this is an architectural change)
- [ ] No unrelated changes mixed into this diff
