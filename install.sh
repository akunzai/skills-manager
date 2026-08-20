#!/usr/bin/env bash
# ==============================================================================
# install.sh - Single-file installer for skills-manager
# Packages a standalone executable directly to ~/.local/bin/skills
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
REPO_URL="https://github.com/akunzai/skills-manager.git"

echo -e "${CYAN}${BOLD}🚀 Installing Skills Manager (skills)...${RESET}\n"

# 1. Check Python 3.10+
if ! command -v python3 >/dev/null 2>&1; then
  echo -e "${RED}❌ Error: python3 is required but not found in PATH.${RESET}" >&2
  exit 1
fi

python_version="$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
py_major="$(echo "$python_version" | cut -d. -f1)"
py_minor="$(echo "$python_version" | cut -d. -f2)"

if [[ "$py_major" -lt 3 || ("$py_major" -eq 3 && "$py_minor" -lt 10) ]]; then
  echo -e "${RED}❌ Error: Python 3.10 or higher is required (found Python ${python_version}).${RESET}" >&2
  exit 1
fi

# 2. Determine source directory
mkdir -p "$TARGET_DIR"
SCRIPT_SOURCE_DIR=""

# If executing from existing local repository
if [[ -f "${BASH_SOURCE[0]:-}" ]]; then
  potential_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -f "${potential_dir}/src/skills_manager/cli.py" ]]; then
    SCRIPT_SOURCE_DIR="${potential_dir}/src"
  fi
fi

TEMP_DIR=""
cleanup() {
  if [[ -n "$TEMP_DIR" && -d "$TEMP_DIR" ]]; then
    rm -rf "$TEMP_DIR"
  fi
}
trap cleanup EXIT

if [[ -z "$SCRIPT_SOURCE_DIR" ]]; then
  # Download / clone repository to temporary directory
  echo -e "📦 Fetching latest skills-manager from GitHub..."
  TEMP_DIR="$(mktemp -d)"
  git clone --depth 1 "$REPO_URL" "${TEMP_DIR}/repo" >/dev/null 2>&1
  SCRIPT_SOURCE_DIR="${TEMP_DIR}/repo/src"
fi

# 3. Package single-file standalone zipapp directly to ~/.local/bin/skills
echo -e "⚙️  Building standalone executable ${BOLD}${TARGET_BIN}${RESET}..."
python3 -m zipapp "$SCRIPT_SOURCE_DIR" -m "skills_manager.cli:main" -p "/usr/bin/env python3" -o "$TARGET_BIN"
chmod +x "$TARGET_BIN"

# Remove any old legacy executable name if present
rm -f "${TARGET_DIR}/skills-manager"

echo -e "\n${GREEN}${BOLD}✨ Installation successful!${RESET}"
echo -e "   Installed at: ${BOLD}${TARGET_BIN}${RESET}"

# 4. Check PATH
if [[ ":$PATH:" != *":${TARGET_DIR}:"* ]]; then
  echo -e "\n${YELLOW}⚠️  Note: ${TARGET_DIR} is not currently in your PATH.${RESET}"
  echo -e "Please add it to your shell configuration (e.g. ~/.zshrc or ~/.bashrc):"
  echo -e "  ${BOLD}export PATH=\"\$HOME/.local/bin:\$PATH\"${RESET}"
fi

echo -e "\n${BOLD}Quick Start:${RESET}"
echo -e "  ${BOLD}skills ls${RESET}            List all installed global skills"
echo -e "  ${BOLD}skills sync${RESET}          Sync and restore skills from ~/.agents/skills.json"
echo -e "  ${BOLD}skills doctor${RESET}        Check and repair global skills health\n"
