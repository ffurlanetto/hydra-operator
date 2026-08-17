BINARY     := hydra-operator
CMD        := ./cmd/operator

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w \
  -X github.com/ffurlanetto/hydra-operator/internal/version.Version=$(VERSION) \
  -X github.com/ffurlanetto/hydra-operator/internal/version.Commit=$(COMMIT) \
  -X github.com/ffurlanetto/hydra-operator/internal/version.BuildDate=$(BUILD_DATE)

ENVTEST_K8S_VERSION := 1.31.0

.PHONY: all build test lint docker helm-lint helm-template deploy-validate rbac-drift-check run clean tidy test-envtest test-e2e e2e-local e2e-hydra-integration

all: build

## build: Build the operator binary.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

## test: Run Go tests with race detector.
test:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...

## lint: Run go vet.
lint:
	go vet ./...

## test-envtest: Run the envtest suite (real kube-apiserver+etcd, no controllers).
## Fetches kubebuilder binaries, Knative CRDs, and the OpenShift Route CRD;
## not part of `test`/CI's default job.
test-envtest:
	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
	mkdir -p test/envtest/crds
	curl -fsSL -o test/envtest/crds/serving-crds.yaml \
	  https://github.com/knative/serving/releases/latest/download/serving-crds.yaml
	OPENSHIFT_API_COMMIT="$$(go list -m -f '{{.Version}}' github.com/openshift/api | sed -E 's/.*-//')"; \
	curl -fsSL -o test/envtest/crds/route-crd.yaml \
	  "https://raw.githubusercontent.com/openshift/api/$${OPENSHIFT_API_COMMIT}/route/v1/zz_generated.crd-manifests/routes.crd.yaml"
	KUBEBUILDER_ASSETS="$$(setup-envtest use $(ENVTEST_K8S_VERSION) -p path)" \
	  go test -tags envtest ./test/envtest/... -v -timeout 5m

## test-e2e: Run the e2e suite against $KUBECONFIG (a real cluster with Knative+Kourier).
## See scripts/e2e-local.sh to stand up that cluster locally, or docs/testing/e2e.md.
test-e2e:
	go test -tags e2e ./test/e2e/... -v -timeout 20m

## e2e-local: Stand up a local kind cluster (+ CRC if present) and run test-e2e against it.
e2e-local:
	./scripts/e2e-local.sh

## e2e-hydra-integration: Real hydra + real hydra-operator + real kind cluster,
## driven purely through Hydra's HTTP API (replaces fakeOperator.ts with the
## real operator process). See scripts/e2e-hydra-integration.sh and
## docs/testing/e2e.md tier 4. Set HYDRA_REPO_PATH to reuse an existing
## ffurlanetto/hydra checkout (default: clones a sibling ../hydra).
e2e-hydra-integration:
	./scripts/e2e-hydra-integration.sh

## docker: Build multi-platform Docker image locally (requires buildx).
docker:
	docker buildx build \
	  --platform linux/amd64,linux/arm64 \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t ghcr.io/ffurlanetto/hydra-operator:$(VERSION) \
	  .

## helm-lint: Lint the Helm chart.
helm-lint:
	helm lint ./helm

## helm-template: Render the Helm chart to stdout with placeholder values.
helm-template:
	helm template hydra-operator ./helm \
	  --set hydra.url=https://hydra.example.com \
	  --set hydra.clusterId=example-cluster

## deploy-validate: Render deploy/base with Kustomize (build-only check, no cluster needed).
deploy-validate:
	kubectl kustomize deploy/base > /dev/null
	kubectl kustomize deploy/overlays/example > /dev/null

## rbac-drift-check: Fail if the ClusterRole differs between deploy/base and helm/.
rbac-drift-check:
	kubectl kustomize deploy/base > /tmp/hydra-operator-kustomize-rendered.yaml
	helm template hydra-operator ./helm \
	  --set hydra.url=https://hydra.example.com \
	  --set hydra.clusterId=example-cluster \
	  > /tmp/hydra-operator-helm-rendered.yaml
	python3 scripts/check-rbac-drift.py \
	  /tmp/hydra-operator-kustomize-rendered.yaml \
	  /tmp/hydra-operator-helm-rendered.yaml

## run: Build and run the operator locally.
run: build
	./$(BINARY)

## tidy: Tidy Go modules.
tidy:
	go mod tidy

## clean: Remove build artifacts.
clean:
	rm -f $(BINARY) coverage.out

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
