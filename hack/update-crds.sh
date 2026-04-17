#!/usr/bin/env bash
# Regenerate the CustomResourceDefinition YAML from Go type annotations.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

CONTROLLER_GEN_VERSION="v0.20.1"
OUTPUT_DIR="config/crd/bases"
mkdir -p "$OUTPUT_DIR"

echo "==> Generating CRDs"
go run sigs.k8s.io/controller-tools/cmd/controller-gen@"$CONTROLLER_GEN_VERSION" \
	crd \
	paths=./pkg/apis/rollouts/v1alpha1/... \
	output:crd:dir="$OUTPUT_DIR"

ls "$OUTPUT_DIR"
