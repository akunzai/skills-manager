package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akunzai/skills-manager/internal/models"
)

type SettingsConfig struct {
	DefaultAgents   []string            `json:"defaultAgents"`
	ExcludeAgents   []string            `json:"excludeAgents"`
	AgentExclusions map[string][]string `json:"agentExclusions"`
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

type PostHook struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Condition   string `json:"condition,omitempty"`
	Run         string `json:"run"`
}

type Config struct {
	Schema    string                 `json:"$schema,omitempty"`
	Version   int                    `json:"version"`
	Settings  SettingsConfig         `json:"settings"`
	Remote    map[string]RemoteRepo  `json:"remote"`
	Local     map[string]LocalEntry  `json:"local"`
	PostHooks []PostHook             `json:"postHooks"`
}

func DefaultConfig() *Config {
	return &Config{
		Schema:  "https://json-schema.org/draft/2020-12/schema",
		Version: 1,
		Settings: SettingsConfig{
			DefaultAgents:   []string{"claude"},
			ExcludeAgents:   []string{},
			AgentExclusions: map[string][]string{},
		},
		Remote:    make(map[string]RemoteRepo),
		Local:     make(map[string]LocalEntry),
		PostHooks: make([]PostHook, 0),
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
	if cfg.PostHooks == nil {
		cfg.PostHooks = make([]PostHook, 0)
	}
	if len(cfg.Settings.DefaultAgents) == 0 {
		cfg.Settings.DefaultAgents = []string{"claude"}
	}
	if cfg.Settings.ExcludeAgents == nil {
		cfg.Settings.ExcludeAgents = make([]string, 0)
	}
	if cfg.Settings.AgentExclusions == nil {
		cfg.Settings.AgentExclusions = make(map[string][]string)
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
	if cfg.PostHooks == nil {
		cfg.PostHooks = make([]PostHook, 0)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config to JSON: %w", err)
	}

	data = append(data, '\n')
	return os.WriteFile(target, data, 0644)
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

	result := make([]string, 0, len(names))
	for sk := range names {
		result = append(result, sk)
	}
	sort.Strings(result)
	return result
}
