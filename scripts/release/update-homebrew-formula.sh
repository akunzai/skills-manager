#!/usr/bin/env bash
# Publishes Formula/skills-manager.rb to the akunzai/homebrew-tap repo for one
# release. Invoked by .github/workflows/release.yml's goreleaser job, after
# GoReleaser has published the GitHub release and its dist/checksums.txt.
#
#   VERSION=vX.Y.Z HOMEBREW_BUMP_TOKEN=... scripts/release/update-homebrew-formula.sh
#
# Skips with a notice, not a failure, when HOMEBREW_BUMP_TOKEN is unset: a
# clone of this repository without the tap wired up must not fail its release.
set -euo pipefail

if [ -z "${HOMEBREW_BUMP_TOKEN:-}" ]; then
  echo "HOMEBREW_BUMP_TOKEN is not set; skipping Homebrew formula update."
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

SHA_DARWIN_AMD64=$(sha_for "skills_darwin_amd64.tar.gz")
SHA_DARWIN_ARM64=$(sha_for "skills_darwin_arm64.tar.gz")
SHA_LINUX_AMD64=$(sha_for "skills_linux_amd64.tar.gz")
SHA_LINUX_ARM64=$(sha_for "skills_linux_arm64.tar.gz")

git clone "https://x-access-token:${HOMEBREW_BUMP_TOKEN}@github.com/akunzai/homebrew-tap.git" homebrew-tap

cat <<RUBY >homebrew-tap/Formula/skills-manager.rb
class SkillsManager < Formula
  desc "Keep skills in sync across AI coding agents"
  homepage "https://akunzai.github.io/skills-manager/"
  license "MIT"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/akunzai/skills-manager/releases/download/${VERSION}/skills_darwin_amd64.tar.gz"
      sha256 "${SHA_DARWIN_AMD64}"
    end
    if Hardware::CPU.arm?
      url "https://github.com/akunzai/skills-manager/releases/download/${VERSION}/skills_darwin_arm64.tar.gz"
      sha256 "${SHA_DARWIN_ARM64}"
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/akunzai/skills-manager/releases/download/${VERSION}/skills_linux_amd64.tar.gz"
      sha256 "${SHA_LINUX_AMD64}"
    end
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/akunzai/skills-manager/releases/download/${VERSION}/skills_linux_arm64.tar.gz"
      sha256 "${SHA_LINUX_ARM64}"
    end
  end

  # homebrew-core ships a different, Node-based formula also named "skills"
  # that installs its own \`skills\` executable.
  conflicts_with "skills", because: "both install a skills executable"

  def install
    bin.install "skills"
  end

  test do
    assert_match "skills-manager #{version}", shell_output("#{bin}/skills version")
  end
end
RUBY

cd homebrew-tap
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add Formula/skills-manager.rb
git commit -m "chore: bump skills-manager to ${VERSION}" || exit 0
git push origin main
