package engine

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
)

// commandSkill is a Skill declared as a command: the one place that knows how
// its declaration Materializes it, and what that means for the rest of the
// engine.
type commandSkill struct {
	name  string
	entry config.LocalEntry
}

// check runs the declared prerequisite. A command without one always passes.
func (c commandSkill) check() error {
	if c.entry.Check == "" {
		return nil
	}
	if _, _, err := RunCmd(c.entry.Check, ""); err != nil {
		return fmt.Errorf("command check %q failed, skipping %s", c.entry.Check, c.name)
	}
	return nil
}

// materialize runs the command and puts the Skill on skillsDir the way its
// declaration says the command delivers it. The caller runs check first.
func (c commandSkill) materialize(skillsDir string) error {
	if c.entry.Output == config.CommandOutputStdout {
		return c.writeOutput(skillsDir)
	}
	return c.install()
}

// syncRestores reports whether a Sync rewrites the Skill's content: true when
// the command prints it, false when an installer owns what it writes.
func (c commandSkill) syncRestores() bool {
	return c.entry.Output == config.CommandOutputStdout
}

// sameSource reports whether entry declares this Skill from the same Source.
// The check is a prerequisite, not part of the Source, and the description
// only words it; the command and how it delivers the Skill are its identity.
func (c commandSkill) sameSource(entry config.LocalEntry) bool {
	return entry.Command == c.entry.Command && entry.Output == c.entry.Output
}

// install runs the installer, which puts the Skill on the Scope itself.
func (c commandSkill) install() error {
	stdout, stderr, err := RunCmd(c.entry.Command, "")
	if err == nil {
		return nil
	}
	msg := stderr
	if msg == "" {
		msg = stdout
	}
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s", msg)
}

// writeOutput runs a command whose stdout is the Skill's SKILL.md and writes
// that output to the Scope skills directory, supplying frontmatter when the
// command printed none. A command that fails or prints nothing leaves the
// Scope copy as it was.
func (c commandSkill) writeOutput(skillsDir string) error {
	name := c.name
	stdout, stderr, err := RunCmd(c.entry.Command, "")
	if err != nil {
		return fmt.Errorf("%s", cmp.Or(stderr, stdout, err.Error()))
	}
	if stdout == "" {
		return fmt.Errorf("command printed nothing to write as %s's SKILL.md", name)
	}
	skillDir := filepath.Join(skillsDir, name)
	// The Skill is a directory this command owns; a link or file in its
	// place is replaced rather than written through.
	if info, err := os.Lstat(skillDir); err == nil && !info.IsDir() {
		if err := os.Remove(skillDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	content := withSkillFrontmatter(stdout, name, c.entry.Description) + "\n"
	tmp, err := os.CreateTemp(skillDir, ".SKILL.md-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(skillDir, "SKILL.md"))
}
