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

echo -e "${CYAN}${BOLD}Installing Skills Manager...${RESET}\n"

mkdir -p "$TARGET_DIR"

# Check if building from local clone
if [[ -f "${BASH_SOURCE[0]:-}" ]]; then
  LOCAL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -f "${LOCAL_ROOT}/cmd/skills/main.go" ]] && command -v go >/dev/null 2>&1; then
    echo -e "Building from local source with Go..."
    rm -f "$TARGET_BIN"
    (cd "$LOCAL_ROOT" && go build -ldflags="-s -w" -o "$TARGET_BIN" ./cmd/skills)
    chmod +x "$TARGET_BIN"
    echo -e "\n${GREEN}${BOLD}Installed Skills Manager.${RESET}"
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
    echo -e "${RED}Error: Unsupported architecture: $ARCH${RESET}" >&2
    exit 1
    ;;
esac

case "$OS" in
  darwin|linux)
    GOOS="$OS"
    ;;
  *)
    echo -e "${RED}Error: Unsupported OS: $OS${RESET}" >&2
    exit 1
    ;;
esac

echo -e "Platform: ${BOLD}${GOOS}_${GOARCH}${RESET}"

# Archive and checksum names mirror .goreleaser.yaml.
ASSET_NAME="skills_${GOOS}_${GOARCH}.tar.gz"
DOWNLOAD_BASE="https://github.com/${GITHUB_REPO}/releases/latest/download"

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo -e "Downloading: ${DIM}${DOWNLOAD_BASE}/${ASSET_NAME}${RESET}"
curl -fsSL "${DOWNLOAD_BASE}/${ASSET_NAME}" -o "${TMP_DIR}/${ASSET_NAME}" || {
  echo -e "${RED}Error: No prebuilt binary found for ${GOOS}_${GOARCH}.${RESET}" >&2
  exit 1
}
curl -fsSL "${DOWNLOAD_BASE}/checksums.txt" -o "${TMP_DIR}/checksums.txt" || {
  echo -e "${RED}Error: Failed to download checksums.txt; refusing to install an unverified binary.${RESET}" >&2
  exit 1
}

EXPECTED="$(awk -v name="$ASSET_NAME" '$2 == name { print $1 }' "${TMP_DIR}/checksums.txt")"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMP_DIR}/${ASSET_NAME}" | awk '{ print $1 }')"
else
  ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ASSET_NAME}" | awk '{ print $1 }')"
fi
if [[ -z "$EXPECTED" || "$EXPECTED" != "$ACTUAL" ]]; then
  echo -e "${RED}Error: Checksum verification failed for ${ASSET_NAME}.${RESET}" >&2
  exit 1
fi
echo -e "Checksum verified."

tar -xzf "${TMP_DIR}/${ASSET_NAME}" -C "$TMP_DIR" skills
mv "${TMP_DIR}/skills" "$TARGET_BIN"

chmod +x "$TARGET_BIN"

echo -e "\n${GREEN}${BOLD}Installed Skills Manager.${RESET}"
echo -e "   Installed at: ${BOLD}${TARGET_BIN}${RESET}\n"

# Check PATH
if [[ ":$PATH:" != *":${TARGET_DIR}:"* ]]; then
  echo -e "${YELLOW}Note: ${TARGET_DIR} is not currently in your PATH.${RESET}"
  echo -e "   Add it by running:"
  echo -e "   ${BOLD}export PATH=\"\$HOME/.local/bin:\$PATH\"${RESET}\n"
fi
