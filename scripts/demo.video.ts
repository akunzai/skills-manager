import { defineVideo } from "tcut";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

const mockSkills = [
  { name: "agents-md", description: "Maintain agent instructions." },
  { name: "tidy-commits", description: "Clean local commit history." },
] as const;

const skillCount = mockSkills.length;
const addedLine = new RegExp(`Added ${skillCount} skill`);
const lsTotal = new RegExp(`Skills \\(${skillCount} total\\)`);
const setupDone = new RegExp(`DEMO_SETUP_OK ${skillCount}`);

// Mock catalog and binary are created here. tcut hide can return from t.run
// while `>` is still on screen, so slow setup must not live in the PTY.

const castDir = mkdtempSync(join(tmpdir(), "skills-manager-demo-"));
const demoDir = join(castDir, "workspace");
const fixtureDir = join(demoDir, "fixture");
const binDir = join(demoDir, "bin");
mkdirSync(binDir, { recursive: true });
for (const skill of mockSkills) {
  const skillDir = join(fixtureDir, "skills", skill.name);
  mkdirSync(skillDir, { recursive: true });
  writeFileSync(
    join(skillDir, "SKILL.md"),
    `---\nname: ${skill.name}\ndescription: ${skill.description}\n---\n`,
  );
}
execFileSync("git", ["init", "-q"], { cwd: fixtureDir });
execFileSync("git", ["add", "."], { cwd: fixtureDir });
execFileSync(
  "git",
  ["-c", "user.name=Demo", "-c", "user.email=demo@example.invalid", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "fixture"],
  { cwd: fixtureDir },
);
execFileSync("go", ["build", "-o", join(binDir, "skills"), "./cmd/skills"], { cwd: repoRoot });

const setupPath = join(tmpdir(), "sm-demo-setup.sh");
writeFileSync(
  setupPath,
  [
    "set -e",
    `export DEMO_DIR=${JSON.stringify(demoDir)}`,
    'export HOME="$DEMO_DIR/home" AGENTS_HOME="$DEMO_DIR/home/.agents"',
    "export SKILLS_SKIP_SELF_UPDATE_CHECK=1",
    'export SKILLS_CACHE_DIR="$DEMO_DIR/cache"',
    'export XDG_STATE_HOME="$DEMO_DIR/state"',
    'export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL="$DEMO_DIR/gitconfig" GIT_CONFIG_COUNT=0',
    "unset GIT_CONFIG",
    `export PATH=${JSON.stringify(binDir)}:"$PATH"`,
    "hash -r",
    'git config --global url."file://$DEMO_DIR/fixture".insteadOf https://github.com/akunzai/agent-skills.git',
    "set +e",
    `echo "DEMO_SETUP_OK ${skillCount}"`,
    "",
  ].join("\n"),
);
process.on("exit", () => {
  try {
    rmSync(castDir, { recursive: true, force: true });
    rmSync(setupPath, { force: true });
  } catch {
    // Cleanup must not replace a successful render with an exit error.
  }
});

export default defineVideo(
  {
    output: "website/demo.gif",
    cast: join(castDir, "demo.cast"),
    theme: "github-dark",
    cols: 88,
    rows: 24,
    fps: 24,
    typingSpeed: 48,
    maxPause: "2.5s",
    waitTimeout: "120s",
    shadow: true,
    title: "skills — one source, every agent",
    requires: ["git", "go"],
  },
  async (t) => {
    await t.hide(async () => {
      await t.type(`. ${JSON.stringify(setupPath)}`);
      await t.enter();
      await t.wait(setupDone, { scope: "screen" });
      await t.clear();
    });

    await t.print("**One source. Every agent.**");
    await t.sleep("1.2s");
    await t.type("skills add akunzai/agent-skills");
    await t.enter();
    await t.wait(/Space to toggle/, { scope: "screen" });
    await t.expect(/agents-md/);
    await t.expect(/tidy-commits/);
    await t.sleep("1s");
    await t.type("a");
    await t.wait(/\[✓\]/, { scope: "screen" });
    await t.sleep("1s");
    await t.enter();
    await t.wait(/Choose a scope:/, { scope: "screen" });
    await t.sleep("1s");
    await t.enter();
    await t.wait(/Agent availability:/, { scope: "screen" });
    await t.sleep("1s");
    await t.enter();
    await t.wait(addedLine, { scope: "screen" });
    await t.wait(/^>$/);

    await t.print("**Inspect availability.**");
    await t.sleep("800ms");
    await t.run("skills ls");
    await t.expect(lsTotal);
    await t.sleep("2.5s");

    await t.print("**Reconcile offline.**");
    await t.sleep("800ms");
    await t.run("skills sync --dry-run");
    await t.expect(/Scope already matches its Config/);
    await t.sleep("1.5s");
    await t.run("skills doctor");
    await t.expect(/Everything is in top condition/);
    await t.sleep("2.5s");
  },
);
