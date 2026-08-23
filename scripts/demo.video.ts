import { defineVideo } from "tcut";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const castDir = mkdtempSync(join(tmpdir(), "skills-manager-demo-"));
const demoDir = join(castDir, "workspace");
process.on("exit", () => {
  try {
    rmSync(castDir, { recursive: true, force: true });
  } catch {
    // Cleanup must not replace a successful render with an exit error.
  }
});

export default defineVideo(
  {
    output: "docs/assets/demo.gif",
    cast: join(castDir, "demo.cast"),
    theme: "github-dark",
    cols: 88,
    rows: 24,
    fps: 24,
    typingSpeed: 48,
    maxPause: "2.5s",
    shadow: true,
    title: "skills — one source, every agent",
    requires: ["git", "go"],
  },
  async (t) => {
    await t.hide(async () => {
      await t.run(`export DEMO_DIR=${JSON.stringify(demoDir)}`);
      await t.run('export HOME="$DEMO_DIR/home" AGENTS_HOME="$DEMO_DIR/home/.agents"');
      await t.run('export SKILLS_CACHE_DIR="$DEMO_DIR/cache"');
      await t.run('export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL="$DEMO_DIR/gitconfig" GIT_CONFIG_COUNT=0');
      await t.run('unset GIT_CONFIG');
      await t.run('mkdir -p "$DEMO_DIR/bin" "$DEMO_DIR/fixture/skills/agents-md" "$DEMO_DIR/fixture/skills/tidy-commits"');
      await t.run('go build -o "$DEMO_DIR/bin/skills" ./cmd/skills');
      await t.run('export PATH="$DEMO_DIR/bin:$PATH"');
      await t.run(`printf '%s\n' '---' 'name: agents-md' 'description: Maintain agent instructions.' '---' > "$DEMO_DIR/fixture/skills/agents-md/SKILL.md"`);
      await t.run(`printf '%s\n' '---' 'name: tidy-commits' 'description: Clean local commit history.' '---' > "$DEMO_DIR/fixture/skills/tidy-commits/SKILL.md"`);
      await t.run('git -C "$DEMO_DIR/fixture" init -q');
      await t.run('git -C "$DEMO_DIR/fixture" add .');
      await t.run('git -C "$DEMO_DIR/fixture" -c user.name=Demo -c user.email=demo@example.invalid -c commit.gpgSign=false -c core.hooksPath=/dev/null commit -qm fixture');
      await t.run('git config --global url."file://$DEMO_DIR/fixture".insteadOf https://github.com/akunzai/agent-skills.git');
      await t.clear();
    });

    await t.print("**One source. Every agent.**");
    await t.sleep("1.2s");
    await t.type("skills add akunzai/agent-skills");
    await t.enter();
    await t.wait(/Space to toggle/, { scope: "screen" });
    await t.sleep("1s");
    await t.type(" ");
    await t.enter();
    await t.wait(/Choose a scope:/, { scope: "screen" });
    await t.sleep("1s");
    await t.enter();
    await t.wait(/Agent availability:/, { scope: "screen" });
    await t.sleep("1s");
    await t.enter();
    await t.wait(/Added 1 skill/, { scope: "screen" });
    await t.wait(/^>$/);
    await t.run("skills ls");
    await t.sleep("3s");
  },
);
