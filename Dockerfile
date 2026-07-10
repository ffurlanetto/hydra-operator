# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /app

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags="-s -w \
      -X github.com/ffurlanetto/hydra-operator/internal/version.Version=${VERSION} \
      -X github.com/ffurlanetto/hydra-operator/internal/version.Commit=${COMMIT} \
      -X github.com/ffurlanetto/hydra-operator/internal/version.BuildDate=${BUILD_DATE}" \
    -o /hydra-operator \
    ./cmd/operator

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /hydra-operator /hydra-operator

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/hydra-operator"]
