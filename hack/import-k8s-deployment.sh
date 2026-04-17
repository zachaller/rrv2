#!/usr/bin/env bash
# Import the upstream Kubernetes Deployment controller into the repo as a fork
# that preserves git history (via `git read-tree --prefix`). Idempotent: re-running
# against a different tag upgrades the fork.
#
# Usage: ./hack/import-k8s-deployment.sh [TAG]
#   TAG defaults to v1.31.9.

set -euo pipefail

TAG="${1:-v1.31.9}"
REMOTE="upstream-k8s"
SUBTREE="pkg/controller/deployment"
DESTINATION="pkg/controller/rollout/_forkedfrom_k8s"

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! git remote | grep -qx "$REMOTE"; then
  git remote add "$REMOTE" https://github.com/kubernetes/kubernetes.git
fi

# Fetch only the requested tag (shallow). We only need the subtree.
git fetch --depth=1 --no-tags "$REMOTE" "refs/tags/$TAG:refs/tags/$TAG"

# If a previous import exists, remove it before re-reading the tree. We keep
# our own ATTRIBUTION.md outside this directory so it survives.
if [[ -d "$DESTINATION" ]]; then
  git rm -rf --quiet "$DESTINATION" || rm -rf "$DESTINATION"
fi

mkdir -p "$(dirname "$DESTINATION")"
git read-tree --prefix="$DESTINATION/" -u "$TAG:$SUBTREE"

commit_sha=$(git rev-parse "$TAG")
cat > "$DESTINATION/ATTRIBUTION.md" <<EOF
# Upstream attribution

Source:   https://github.com/kubernetes/kubernetes
Path:     $SUBTREE
Tag:      $TAG
Commit:   $commit_sha
License:  Apache-2.0 (see repository LICENSE and NOTICE)

Do not hand-edit files in this directory outside a mechanical rename pass.
Re-import with \`hack/import-k8s-deployment.sh [TAG]\`.
EOF

git add "$DESTINATION"
echo "Imported $REMOTE $TAG:$SUBTREE -> $DESTINATION"
