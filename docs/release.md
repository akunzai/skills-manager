# Release SOP for Skills Manager

Standard operating procedure (SOP) for releasing new versions of `skills-manager`.

## Pipeline Overview

The release pipeline is automated via GitHub Actions (`.github/workflows/release.yml`). Pushing a Git tag (`vX.Y.Z`) triggers:
1. Multi-version test execution.
2. Building standalone zipapp executable `skills`.
3. Generating categorized release notes via `.github/release.yml` and attaching the `skills` standalone binary asset.

## Release Notes Categorization

Release notes are automatically categorized from merged PR labels (applied automatically via `.github/workflows/pr-labeler.yml` from Conventional Commits prefix):

- `feat: ...` -> 🚀 **Features & Enhancements** (`enhancement`)
- `fix: ...` -> 🐛 **Bug Fixes** (`bug`)
- `docs: ...` -> 📚 **Documentation** (`documentation`)
- `ci: ...` -> ⚙️ **CI/CD & Build** (`ci`)
- `chore: ...` / `refactor: ...` -> 🧹 **Refactoring & Chores** (`chore`, `refactor`)
- `dependencies` -> 📦 **Dependency Updates** (`dependencies`)
- `test: ...` -> 🧪 **Tests** (`tests`)

## Step-by-Step Release Checklist

### 1. Pre-flight Checks
Ensure test suite passes and working tree is clean:
```bash
PYTHONPATH=src python3 -m unittest discover -s tests -v
git status
```

### 2. Bump Version Number (Single Source of Truth)
Update the version string only in `src/skills_manager/__init__.py`:
- `src/skills_manager/__init__.py`: `__version__ = "X.Y.Z"`

*(Note: `pyproject.toml` uses dynamic versioning pointing to `src/skills_manager/__init__.py` as the single source of truth).*

### 3. Commit and Tag
```bash
git add src/skills_manager/__init__.py
git commit -m "chore(release): bump version to vX.Y.Z"
git tag -a vX.Y.Z -m "Release vX.Y.Z"
```

### 4. Push Commit & Tag
```bash
git push origin main
git push origin vX.Y.Z
```

### 5. Verification
1. Check GitHub Actions run under the Actions tab.
2. Confirm the release is published at `https://github.com/akunzai/skills-manager/releases`.
3. Verify self-update detects the new release:
   ```bash
   skills self-update --check
   ```

### 6. Milestone Management
1. **Close the released milestone**:
   ```bash
   # Find milestone number
   gh api repos/akunzai/skills-manager/milestones
   # Close it
   gh api repos/akunzai/skills-manager/milestones/<number> --method PATCH -f state="closed"
   ```
2. **Create the next milestone** (if not already created):
   ```bash
   gh api repos/akunzai/skills-manager/milestones --method POST -f title="X.Y+1.0" -f description="Next feature release."
   ```
