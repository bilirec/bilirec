#!/usr/bin/env bash
set -euo pipefail

EVENT_NAME="${1:-}"
REF="${2:-}"
RELEASE_TAG="${3:-}"

GITHUB_REPO="bilirec/bilirec"
DEFAULT_VERSION="v0.1.8"

TAG=""

if [ "$EVENT_NAME" = "release" ] && [ -n "$RELEASE_TAG" ]; then
  TAG="$RELEASE_TAG"
elif [[ "$REF" == refs/tags/* ]]; then
  TAG="${REF#refs/tags/}"
else
  TAG=$(gh release view --repo "$GITHUB_REPO" --json tagName -q .tagName 2>/dev/null || echo "")
fi

if [ -z "$TAG" ]; then
  TAG="$DEFAULT_VERSION"
fi

if [[ ! "$TAG" =~ ^[vV] ]]; then
  TAG="v${TAG}"
fi

CORE="${TAG#v}"
CORE="${CORE#V}"
if ! echo "$CORE" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "::error::Release tag 格式必須為 vX.Y.Z，實際為：$TAG" >&2
  exit 1
fi

echo "v${CORE}"
