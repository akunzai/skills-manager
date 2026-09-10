# Release SOP for Skills Manager

Standard operating procedure (SOP) for releasing new versions of `skills-manager`.

## Pipeline Overview

The release pipeline is automated via GitHub Actions (`.github/workflows/release.yml`) and GoReleaser (`.goreleaser.yaml`). Pushing a Git tag (`vX.Y.Z`) triggers:
1. Multi-platform cross-compilation for Linux, macOS, and Windows (arm64 & amd64).
2. Generating checksums and GitHub release assets (`.tar.gz` and `.zip`).
3. Generating categorized release notes.

## Release Notes Categorization

Release notes list **merged pull requests, grouped by label**. Three pieces cooperate:

1. `.github/workflows/pr-labeler.yml` reads the **branch name** prefix and applies a label
   (`feat/` -> `enhancement`, `fix/` -> `bug`, `docs/` -> `documentation`, `ci/` /
   `build/` -> `ci`, `chore/` / `release/` -> `chore`, `refactor/` -> `refactor`,
   `test/` -> `tests`, `perf/` -> `enhancement`). An unrecognized prefix leaves labels
   untouched, so a label applied by hand survives — which is also why Dependabot keeps
   the `dependencies` label it applies to its own `dependabot/...` branches.
2. `.github/release.yml` maps those labels to release-note sections, and drops PRs
   labelled `wontfix`, `duplicate`, or `invalid`.
3. `.goreleaser.yaml` sets `changelog.use: github-native`, handing note generation to
   GitHub so the two files above are what actually decide the output.

| Label | Section |
| :--- | :--- |
| `enhancement`, `feature`, `feat` | 🚀 Features & Enhancements |
| `bug`, `fix` | 🐛 Bug Fixes |
| `dependencies` | 📦 Dependency Updates |
| `documentation`, `docs` | 📚 Documentation |
| `ci`, `build`, `github-actions` | ⚙️ CI/CD & Build |
| `refactor`, `chore`, `maintenance` | 🧹 Refactoring & Chores |
| `tests`, `test` | 🧪 Tests |
| anything else (`*`) | 💬 Other Changes |

Because `github-native` is in use, GoReleaser's own `groups`, `sort`, `filters`, and
`abbrev` settings are ignored — don't add them back expecting an effect.

### What this means in practice

- **Neither commit messages nor the PR title affect categorization.** A PR carrying a
  mix of `fix:` and `docs:` commits is filed once, under its own label.
- **Name the branch with a category prefix.** The branch name is the labeler's only
  input. PR titles are free-form and need no Conventional Commits prefix; the title is
  what becomes the release-note entry, so write it for a reader.
- **A PR that spans categories gets one label.** Pick the prefix matching its primary
  intent, or relabel by hand after opening it.
- **Labels are read when the notes are generated, not when the PR merges.** A PR that
  landed with the wrong label, or none, can be relabelled afterwards and the notes
  regenerated. `.github/release.yml` is also resolved from the ref being released, not
  from the default branch.
- **Commits pushed straight to `main` never appear.** They belong to no PR, so a fix
  landed outside the PR flow is silently missing from the notes. Either route it through
  a PR, or patch the published release afterwards:

  ```bash
  gh api repos/akunzai/skills-manager/releases/tags/vX.Y.Z --jq .id
  gh api repos/akunzai/skills-manager/releases/<id> --method PATCH -f body="..."
  ```

- **Preview before tagging** to see exactly what will be published:

  ```bash
  gh api repos/akunzai/skills-manager/releases/generate-notes \
    -f tag_name=vX.Y.Z -f target_commitish=main -f previous_tag_name=vX.Y.(Z-1) \
    --jq .body
  ```

## Step-by-Step Release Checklist

### 1. Refresh User-Facing Demo

Rebuild the README demo when its commands, prompts, order, text, layout, icons, colors, workflow, flags, or relevant configuration schema change, unless it was already regenerated and reviewed with those changes:

```bash
mise install
mise run demo
```

Review the GIF before committing it. At release time, reuse an already reviewed demo that covers the user-visible changes; do not regenerate it again. Skip regeneration when a release has no user-visible CLI change.

### 2. Pre-flight Checks
Ensure test suite passes and working tree is clean:
```bash
go test -v ./...
git status
```

### 3. Bump Version Number
Update the version string in `internal/updater/updater.go`:
- `internal/updater/updater.go`: `var Version = "X.Y.Z"`

### 4. Commit and Tag
```bash
git add internal/updater/updater.go
git commit -m "chore(release): bump version to vX.Y.Z"
git tag -a vX.Y.Z -m "Release vX.Y.Z"
```

### 5. Push Commit & Tag
```bash
git push origin main
git push origin vX.Y.Z
```

### 6. Verification
1. Check GitHub Actions run under the Actions tab.
2. Confirm the release is published at `https://github.com/akunzai/skills-manager/releases`.
3. Verify self-update detects the new release:
   ```bash
   skills self-update --check
   ```

### 7. Milestone Management
1. **Summarize the released milestone** with one concise sentence describing its main user-facing outcomes:
   ```bash
   gh api repos/akunzai/skills-manager/milestones/<number> --method PATCH \
     -f description="<release highlights>."
   ```
2. **Close the released milestone**:
   ```bash
   # Find milestone number
   gh api repos/akunzai/skills-manager/milestones
   # Close it
   gh api repos/akunzai/skills-manager/milestones/<number> --method PATCH -f state="closed"
   ```
3. **Create the next milestone** (if not already created):
   ```bash
   gh api repos/akunzai/skills-manager/milestones --method POST -f title="X.Y+1.0" -f description="Next feature release."
   ```
