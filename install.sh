#!/usr/bin/env bash
# ==============================================================================
# install.sh - Standalone installer for skills-manager (Go)
# Downloads or builds the single-file binary directly to ~/.local/bin/skills
# ==============================================================================
set -euo pipefail

# ANSI color codes
CYAN='\033[96m'
GREEN='\033[92m'
YELLOW='\033[93m'
RED='\033[91m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

TARGET_DIR="${HOME}/.local/bin"
TARGET_BIN="${TARGET_DIR}/skills"
GITHUB_REPO="akunzai/skills-manager"

echo -e "${CYAN}${BOLD}🚀 Installing Skills Manager (skills)...${RESET}\n"

mkdir -p "$TARGET_DIR"

# Check if building from local clone
if [[ -f "${BASH_SOURCE[0]:-}" ]]; then
  LOCAL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -f "${LOCAL_ROOT}/cmd/skills/main.go" ]] && command -v go >/dev/null 2>&1; then
    echo -e "⚙️  Building from local source with Go..."
    rm -f "$TARGET_BIN"
    (cd "$LOCAL_ROOT" && go build -ldflags="-s -w" -o "$TARGET_BIN" ./cmd/skills)
    chmod +x "$TARGET_BIN"
    echo -e "\n${GREEN}${BOLD}✨ Installation successful!${RESET}"
    echo -e "   Installed at: ${BOLD}${TARGET_BIN}${RESET}\n"
    exit 0
  fi
fi

# Detect OS and Architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64)
    GOARCH="amd64"
    ;;
  arm64|aarch64)
    GOARCH="arm64"
    ;;
  *)
    echo -e "${RED}❌ Unsupported architecture: $ARCH${RESET}" >&2
    exit 1
    ;;
esac

case "$OS" in
  darwin|linux)
    GOOS="$OS"
    ;;
  *)
    echo -e "${RED}❌ Unsupported OS: $OS${RESET}" >&2
    exit 1
    ;;
esac

echo -e "🔍 Detected platform: ${BOLD}${GOOS}_${GOARCH}${RESET}"

# Fetch latest release info from GitHub API
RELEASE_API="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"
echo -e "📦 Fetching latest release information..."

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

RELEASE_JSON="${TMP_DIR}/release.json"
curl -fsSL -H "Accept: application/vnd.github.v3+json" "$RELEASE_API" -o "$RELEASE_JSON" || {
  echo -e "${RED}❌ Failed to fetch release metadata from GitHub.${RESET}" >&2
  exit 1
}

# Find download URL for the platform archive
ASSET_URL="$(grep "browser_download_url" "$RELEASE_JSON" | grep -i "${GOOS}" | grep -i "${GOARCH}" | cut -d '"' -f 4 | head -n 1)"

if [[ -z "$ASSET_URL" ]]; then
  # Fallback to direct 'skills' binary asset if available
  ASSET_URL="$(grep "browser_download_url" "$RELEASE_JSON" | grep -E '"[^"]*/skills"' | cut -d '"' -f 4 | head -n 1)"
fi

if [[ -z "$ASSET_URL" ]]; then
  echo -e "${RED}❌ No prebuilt binary found for ${GOOS}_${GOARCH}.${RESET}" >&2
  exit 1
fi

echo -e "📥 Downloading: ${DIM}${ASSET_URL}${RESET}"
ARCHIVE_FILE="${TMP_DIR}/downloaded"
curl -fsSL "$ASSET_URL" -o "$ARCHIVE_FILE"

if [[ "$ASSET_URL" == *.tar.gz || "$ASSET_URL" == *.tgz ]]; then
  tar -xzf "$ARCHIVE_FILE" -C "$TMP_DIR"
  mv "${TMP_DIR}/skills" "$TARGET_BIN"
elif [[ "$ASSET_URL" == *.zip ]]; then
  unzip -q -o "$ARCHIVE_FILE" -d "$TMP_DIR"
  mv "${TMP_DIR}/skills" "$TARGET_BIN"
else
  mv "$ARCHIVE_FILE" "$TARGET_BIN"
fi

chmod +x "$TARGET_BIN"

echo -e "\n${GREEN}${BOLD}✨ Installation successful!${RESET}"
echo -e "   Installed at: ${BOLD}${TARGET_BIN}${RESET}\n"

# Check PATH
if [[ ":$PATH:" != *":${TARGET_DIR}:"* ]]; then
  echo -e "${YELLOW}⚠️  Note: ${TARGET_DIR} is not currently in your PATH.${RESET}"
  echo -e "   Add it by running:"
  echo -e "   ${BOLD}export PATH=\"\$HOME/.local/bin:\$PATH\"${RESET}\n"
fi
