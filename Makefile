BINARY     := hydra-operator
CMD        := ./cmd/operator

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w \
  -X github.com/ffurlanetto/hydra-operator/internal/version.Version=$(VERSION) \
  -X github.com/ffurlanetto/hydra-operator/internal/version.Commit=$(COMMIT) \
  -X github.com/ffurlanetto/hydra-operator/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build test lint docker helm-lint run clean tidy

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

## docker: Build multi-platform Docker image locally (requires buildx).
docker:
	docker buildx build \
	  --platform linux/amd64,linux/arm64 \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t ghcr.io/ffurlanetto/hydra-operator:$(VERSION) \
	  .

## helm-lint: Lint the Helm chart (added in Epic 2/OP-014).
helm-lint:
	@if [ -d helm ]; then helm lint ./helm; else echo "helm chart not present yet"; fi

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
