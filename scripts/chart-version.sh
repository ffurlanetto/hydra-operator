#!/usr/bin/env bash
# Derives a valid Helm chart version + appVersion for helm-package (Makefile
# and docker.yml's helm-package job share this, so the two never drift):
# an exact git tag (vX.Y.Z) when HEAD is one, otherwise Chart.yaml's static
# base version plus the commit SHA as SemVer build metadata. The build
# metadata fallback is always valid SemVer regardless of whether the repo
# has any tags at all yet — `git describe --tags --always` alone isn't:
# with zero reachable tags it falls back to a bare SHA, which `helm
# package --version` rejects as "Invalid Semantic Version".
set -euo pipefail

if TAG=$(git describe --tags --exact-match 2>/dev/null); then
  CHART_VERSION="${TAG#v}"
  APP_VERSION="$TAG"
else
  BASE_VERSION=$(grep '^version:' helm/Chart.yaml | awk '{print $2}' | tr -d '"')
  SHA=$(git rev-parse --short HEAD)
  CHART_VERSION="${BASE_VERSION}+${SHA}"
  APP_VERSION="$CHART_VERSION"
fi

echo "CHART_VERSION=${CHART_VERSION}"
echo "APP_VERSION=${APP_VERSION}"
