#!/usr/bin/env python3
"""Fail if the hydra-operator ClusterRole differs between the rendered
Kustomize manifests and the rendered Helm chart.

Both manifest sets are meant to grant identical RBAC (see
deploy/base/clusterrole.yaml and helm/templates/clusterrole.yaml) — this is
the safeguard that keeps them from silently diverging on a security-critical
permission set when only one side gets updated.

Usage: check-rbac-drift.py <kustomize-rendered.yaml> <helm-rendered.yaml>
"""
import sys

import yaml

CLUSTERROLE_NAME = "hydra-operator"


def load_clusterrole(path):
    with open(path) as f:
        docs = list(yaml.safe_load_all(f))
    matches = [
        d
        for d in docs
        if d
        and d.get("kind") == "ClusterRole"
        and d.get("metadata", {}).get("name") == CLUSTERROLE_NAME
    ]
    if not matches:
        sys.exit(f"no ClusterRole/{CLUSTERROLE_NAME} found in {path}")
    if len(matches) > 1:
        sys.exit(f"multiple ClusterRole/{CLUSTERROLE_NAME} found in {path}")
    return matches[0]


def normalize_rules(clusterrole):
    rules = clusterrole.get("rules", [])
    normalized = []
    for rule in rules:
        normalized.append(
            tuple(
                sorted(
                    (key, tuple(sorted(values)))
                    for key, values in rule.items()
                )
            )
        )
    return sorted(normalized)


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)

    kustomize_path, helm_path = sys.argv[1], sys.argv[2]
    kustomize_rules = normalize_rules(load_clusterrole(kustomize_path))
    helm_rules = normalize_rules(load_clusterrole(helm_path))

    if kustomize_rules == helm_rules:
        print(f"OK: ClusterRole/{CLUSTERROLE_NAME} rules match between Kustomize and Helm")
        return

    only_kustomize = [r for r in kustomize_rules if r not in helm_rules]
    only_helm = [r for r in helm_rules if r not in kustomize_rules]
    print(f"DRIFT: ClusterRole/{CLUSTERROLE_NAME} rules differ between Kustomize and Helm")
    if only_kustomize:
        print("\nOnly in deploy/base/clusterrole.yaml:")
        for r in only_kustomize:
            print(f"  {dict(r)}")
    if only_helm:
        print("\nOnly in helm/templates/clusterrole.yaml:")
        for r in only_helm:
            print(f"  {dict(r)}")
    sys.exit(1)


if __name__ == "__main__":
    main()
