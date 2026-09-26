#!/usr/bin/env bash
# Publishes bucket/skills-manager.json to the akunzai/scoop-bucket repo for
# one release. Invoked by .github/workflows/release.yml's goreleaser job,
# after GoReleaser has published the GitHub release and its
# dist/checksums.txt.
#
#   VERSION=vX.Y.Z HOMEBREW_BUMP_TOKEN=... scripts/release/update-scoop-manifest.sh
#
# Skips with a notice, not a failure, when HOMEBREW_BUMP_TOKEN is unset: a
# clone of this repository without the bucket wired up must not fail its
# release.
#
# Only the fields that change per release are patched in place; checkver,
# autoupdate, description and the rest of bucket/skills-manager.json are
# maintained by hand in the bucket repo.
set -euo pipefail

if [ -z "${HOMEBREW_BUMP_TOKEN:-}" ]; then
	echo "HOMEBREW_BUMP_TOKEN is not set; skipping Scoop manifest update."
	exit 0
fi

VERSION="${VERSION:?VERSION must be set (e.g. v0.18.0)}"
CHECKSUMS="${CHECKSUMS:-dist/checksums.txt}"

sha_for() {
	awk -v f="$1" '
		$2 == f { print $1; found = 1 }
		END { if (!found) { print "checksum not found for " f > "/dev/stderr"; exit 1 } }
	' "$CHECKSUMS"
}

SHA_WIN_AMD64=$(sha_for "skills_windows_amd64.zip")
SHA_WIN_ARM64=$(sha_for "skills_windows_arm64.zip")

git clone "https://x-access-token:${HOMEBREW_BUMP_TOKEN}@github.com/akunzai/scoop-bucket.git" scoop-bucket

tmp=$(mktemp)
jq --indent 4 \
	--arg version "${VERSION#v}" \
	--arg url_amd64 "https://github.com/akunzai/skills-manager/releases/download/${VERSION}/skills_windows_amd64.zip" \
	--arg hash_amd64 "$SHA_WIN_AMD64" \
	--arg url_arm64 "https://github.com/akunzai/skills-manager/releases/download/${VERSION}/skills_windows_arm64.zip" \
	--arg hash_arm64 "$SHA_WIN_ARM64" \
	'.version = $version
	| .architecture["64bit"].url = $url_amd64
	| .architecture["64bit"].hash = $hash_amd64
	| .architecture.arm64.url = $url_arm64
	| .architecture.arm64.hash = $hash_arm64' \
	scoop-bucket/bucket/skills-manager.json >"$tmp"
mv "$tmp" scoop-bucket/bucket/skills-manager.json

cd scoop-bucket
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add bucket/skills-manager.json
git commit -m "chore: bump skills-manager to ${VERSION}" || exit 0
git push origin main
