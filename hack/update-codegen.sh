#!/usr/bin/env bash
# Regenerate deepcopy methods (and later: clientset/listers/informers) for the
# rollouts.io/v1alpha1 API. Runs controller-gen via `go run` so we don't pin
# the binary as a tools.go import.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

# controller-gen version is resolved from go.sum via go run.
CONTROLLER_GEN_VERSION="v0.20.1"

echo "==> Generating deepcopy methods"
go run sigs.k8s.io/controller-tools/cmd/controller-gen@"$CONTROLLER_GEN_VERSION" \
	object:headerFile=hack/boilerplate.go.txt \
	paths=./pkg/apis/rollouts/v1alpha1/...

echo "Done."
