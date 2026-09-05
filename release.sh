#!/bin/sh
# Create a GitHub Release locally (tag + goreleaser).
# Usage: ./release.sh v0.1.0
# Optional: SKIP_TESTS=1 SKIP_PUSH=1 ./release.sh v0.1.0

set -eu

fail() {
	printf 'release.sh: %s\n' "$*" >&2
	exit 1
}

tag=${1:-}
[ -n "$tag" ] || fail "usage: $0 vX.Y.Z"

echo "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$' ||
	fail "invalid tag: $tag (expected SemVer like v1.2.3 or v1.2.3-rc.1)"

root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
cd "$root"

command -v git >/dev/null 2>&1 || fail "git is required"
command -v goreleaser >/dev/null 2>&1 || fail "goreleaser is required (go install github.com/goreleaser/goreleaser/v2@latest)"
command -v gh >/dev/null 2>&1 || fail "gh is required"

if [ -z "${SKIP_TESTS:-}" ]; then
	printf 'Running tests...\n'
	go test ./...
fi

# Ensure we are on (or can create) the release tag pointing at HEAD.
if git rev-parse "$tag" >/dev/null 2>&1; then
	printf 'Tag %s already exists locally.\n' "$tag"
else
	printf 'Creating annotated tag %s...\n' "$tag"
	git tag -a "$tag" -m "$tag"
fi

if [ -z "${SKIP_PUSH:-}" ]; then
	printf 'Pushing tag %s...\n' "$tag"
	git push origin "$tag"
fi

export GITHUB_TOKEN="${GITHUB_TOKEN:-$(gh auth token)}"
[ -n "$GITHUB_TOKEN" ] || fail "GITHUB_TOKEN is empty (run: gh auth login)"

printf 'Checking GoReleaser config...\n'
goreleaser check

printf 'Publishing GitHub Release for %s...\n' "$tag"
goreleaser release --clean

printf '\nRelease %s published.\n' "$tag"
gh release view "$tag" --web 2>/dev/null || gh release view "$tag"
