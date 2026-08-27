package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/akunzai/skills-manager/internal/models"
)

type SettingsConfig struct {
	DefaultAgents []string                        `json:"defaultAgents"`
	Availability  map[string]AvailabilityOverride `json:"availability,omitempty"`
}

type AvailabilityOverride struct {
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

type RemoteRepo struct {
	Type   string            `json:"type"`
	URL    string            `json:"url,omitempty"`
	Branch string            `json:"branch,omitempty"`
	Skills map[string]string `json:"skills"`
}

type LocalEntry struct {
	Type        string `json:"type"` // "symlink", "command"
	Source      string `json:"source,omitempty"`
	Command     string `json:"command,omitempty"`
	Check       string `json:"check,omitempty"`
	Description string `json:"description,omitempty"`
}

type Config struct {
	Schema   string                `json:"$schema,omitempty"`
	Version  int                   `json:"version"`
	Settings SettingsConfig        `json:"settings"`
	Remote   map[string]RemoteRepo `json:"remote"`
	Local    map[string]LocalEntry `json:"local"`
}

// DefaultSchemaURL points at this project's config schema so editors can
// validate and complete skills.json. It is not the JSON Schema meta-schema.
const DefaultSchemaURL = "https://raw.githubusercontent.com/akunzai/skills-manager/main/skills.schema.json"

func DefaultConfig() *Config {
	return &Config{
		Schema:  DefaultSchemaURL,
		Version: 1,
		Settings: SettingsConfig{
			DefaultAgents: []string{"claude"},
			Availability:  map[string]AvailabilityOverride{},
		},
		Remote: make(map[string]RemoteRepo),
		Local:  make(map[string]LocalEntry),
	}
}

func LoadConfig(configPath string) (*Config, error) {
	target := configPath
	if target == "" {
		target = models.DefaultConfigFile()
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("failed to read skills config from %s: %w", target, err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse skills config from %s: %w", target, err)
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Remote == nil {
		cfg.Remote = make(map[string]RemoteRepo)
	}
	if cfg.Local == nil {
		cfg.Local = make(map[string]LocalEntry)
	}
	if len(cfg.Settings.DefaultAgents) == 0 {
		cfg.Settings.DefaultAgents = []string{"claude"}
	}
	if cfg.Settings.Availability == nil {
		cfg.Settings.Availability = make(map[string]AvailabilityOverride)
	}
	if err := NormalizeAvailability(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func SaveConfig(cfg *Config, configPath string) error {
	target := configPath
	if target == "" {
		target = models.DefaultConfigFile()
	}

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("failed to create directory for config: %w", err)
	}

	// Sort remote and local keys deterministically
	if cfg.Remote == nil {
		cfg.Remote = make(map[string]RemoteRepo)
	}
	if cfg.Local == nil {
		cfg.Local = make(map[string]LocalEntry)
	}
	if err := NormalizeAvailability(cfg); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config to JSON: %w", err)
	}

	data = append(data, '\n')
	return os.WriteFile(target, data, 0644)
}

func NormalizeAvailability(cfg *Config) error {
	if cfg.Settings.Availability == nil {
		cfg.Settings.Availability = make(map[string]AvailabilityOverride)
		return nil
	}
	for skill, override := range cfg.Settings.Availability {
		override.Include = normalizeAgents(override.Include)
		override.Exclude = normalizeAgents(override.Exclude)
		excluded := make(map[string]struct{}, len(override.Exclude))
		for _, agent := range override.Exclude {
			excluded[agent] = struct{}{}
		}
		for _, agent := range override.Include {
			if _, conflict := excluded[agent]; conflict {
				return fmt.Errorf("invalid availability for skill %q: agent %q is both included and excluded", skill, agent)
			}
		}
		if len(override.Include) == 0 && len(override.Exclude) == 0 {
			delete(cfg.Settings.Availability, skill)
			continue
		}
		cfg.Settings.Availability[skill] = override
	}
	return nil
}

func normalizeAgents(agents []string) []string {
	seen := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		if norm := models.NormalizeAgentName(agent); norm != "" {
			seen[norm] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

func FindSkillSource(cfg *Config, skillName string) (category string, sourceKey string, found bool) {
	for srcKey, repo := range cfg.Remote {
		if _, ok := repo.Skills[skillName]; ok {
			return "remote", srcKey, true
		}
	}
	if _, ok := cfg.Local[skillName]; ok {
		return "local", skillName, true
	}
	return "", "", false
}

func AddRemoteSkillEntry(
	cfg *Config,
	source string,
	skillName string,
	subpath string,
	repoType string,
	url string,
) {
	// Clean up any existing registration for this skill name across local and other remotes
	removeSkillRegistration(cfg, skillName)

	if cfg.Remote == nil {
		cfg.Remote = make(map[string]RemoteRepo)
	}
	if repoType == "" {
		repoType = "github"
	}

	entry, ok := cfg.Remote[source]
	if !ok {
		entry = RemoteRepo{
			Type:   repoType,
			URL:    url,
			Skills: make(map[string]string),
		}
	}
	if url != "" {
		entry.URL = url
	}
	entry.Type = repoType
	if entry.Skills == nil {
		entry.Skills = make(map[string]string)
	}
	entry.Skills[skillName] = subpath
	cfg.Remote[source] = entry
}

func AddLocalSymlinkEntry(
	cfg *Config,
	skillName string,
	sourcePath string,
	description string,
) {
	removeSkillRegistration(cfg, skillName)
	if cfg.Local == nil {
		cfg.Local = make(map[string]LocalEntry)
	}
	cfg.Local[skillName] = LocalEntry{
		Type:        "symlink",
		Source:      sourcePath,
		Description: description,
	}
}

func AddLocalCommandEntry(
	cfg *Config,
	skillName string,
	command string,
	checkCmd string,
	description string,
) {
	removeSkillRegistration(cfg, skillName)
	if cfg.Local == nil {
		cfg.Local = make(map[string]LocalEntry)
	}
	cfg.Local[skillName] = LocalEntry{
		Type:        "command",
		Command:     command,
		Check:       checkCmd,
		Description: description,
	}
}

func RemoveSkillEntry(cfg *Config, skillName string) bool {
	found := removeSkillRegistration(cfg, skillName)
	if _, ok := cfg.Settings.Availability[skillName]; ok {
		delete(cfg.Settings.Availability, skillName)
		found = true
	}
	return found
}

func removeSkillRegistration(cfg *Config, skillName string) bool {
	found := false

	// Check remote
	for srcKey, repo := range cfg.Remote {
		if _, ok := repo.Skills[skillName]; ok {
			delete(repo.Skills, skillName)
			found = true
			if len(repo.Skills) == 0 {
				delete(cfg.Remote, srcKey)
			} else {
				cfg.Remote[srcKey] = repo
			}
		}
	}

	// Check local
	if _, ok := cfg.Local[skillName]; ok {
		delete(cfg.Local, skillName)
		found = true
	}

	return found
}

func GetConfiguredSkillNames(cfg *Config) []string {
	names := make(map[string]struct{})
	for _, repo := range cfg.Remote {
		for sk := range repo.Skills {
			names[sk] = struct{}{}
		}
	}
	for sk := range cfg.Local {
		names[sk] = struct{}{}
	}

	return slices.Sorted(maps.Keys(names))
}
