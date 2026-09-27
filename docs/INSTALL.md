# Installation

Each [release](https://github.com/akunzai/skills-manager/releases/latest) attaches
prebuilt, checksummed binaries — no Go toolchain required.

## Download a prebuilt binary (recommended)

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/akunzai/skills-manager/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/akunzai/skills-manager/main/install.ps1 | iex
```

The installers detect your platform, download the matching release asset, and verify it
against the release's `checksums.txt`.

Every release archive also carries a build provenance attestation:

```sh
gh attestation verify skills_<os>_<arch>.tar.gz --repo akunzai/skills-manager
```

## Homebrew (macOS / Linux)

```sh
brew install akunzai/tap/skills-manager
```

Upgrade with `brew upgrade akunzai/tap/skills-manager`.

## Scoop (Windows)

```powershell
scoop bucket add akunzai https://github.com/akunzai/scoop-bucket
scoop install akunzai/skills-manager
```

Upgrade with `scoop update skills-manager`.

## mise

[mise](https://mise.jdx.dev) can install and version-manage `skills-manager` through its
built-in `github` backend, which reuses the same checksummed release binaries and verifies
their GitHub artifact attestation automatically — no Go toolchain needed:

```sh
mise use -g github:akunzai/skills-manager   # latest; pin with @X.Y.Z
```

Drop `-g` to activate it for one project instead. Upgrade with `mise upgrade
github:akunzai/skills-manager`.

## Upgrading

If you installed via `install.sh`, `install.ps1`, or a manual GitHub Release download:

```sh
skills self-update
```

On a terminal, other commands mention a newer release at most once a day. A Homebrew,
Scoop, or mise install refuses to replace itself and names that package manager's upgrade
command instead — `skills self-update` detects which one owns the running binary and
prints the right command rather than clobbering what it manages.

`self-update` checks each download against the release's `checksums.txt` the same way the
installers do.
