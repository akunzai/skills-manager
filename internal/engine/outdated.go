package engine

import "github.com/akunzai/skills-manager/internal/config"

type OutdatedRepository struct {
	Source    string           `json:"source"`
	URL       string           `json:"url"`
	Branch    string           `json:"branch"`
	Status    string           `json:"status"`
	LocalSHA  string           `json:"local_sha"`
	RemoteSHA string           `json:"remote_sha"`
	CachePath string           `json:"cache_path"`
	Error     string           `json:"error,omitempty"`
	Skills    []SkillFreshness `json:"skills"`
}

type OutdatedReport struct {
	Repositories []OutdatedRepository `json:"repositories"`
	StateError   string               `json:"state_error,omitempty"`
}

func InspectOutdated(cfg *config.Config, skillsDir, cacheDir string, workers int) (*OutdatedReport, error) {
	plan, err := PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if err != nil {
		return nil, err
	}
	statuses := CheckAllRemoteSkillsOutdated(cfg, cacheDir, workers)
	bySource := make(map[string]UpdateStatusResult, len(statuses))
	for _, status := range statuses {
		bySource[status.Source] = status
	}
	report := &OutdatedReport{StateError: plan.StateError}
	for _, repository := range plan.Repositories {
		status := bySource[repository.Source]
		report.Repositories = append(report.Repositories, OutdatedRepository{
			Source: status.Source, URL: status.URL, Branch: status.Branch,
			Status: status.Status, LocalSHA: status.LocalSHA, RemoteSHA: status.RemoteSHA,
			CachePath: status.CachePath, Error: status.Error, Skills: repository.Skills,
		})
	}
	return report, nil
}

func (report *OutdatedReport) Fresh() bool {
	if report == nil || report.StateError != "" {
		return false
	}
	for _, repository := range report.Repositories {
		if repository.Status != "up_to_date" {
			return false
		}
		for _, skill := range repository.Skills {
			if skill.Status != SkillInSync {
				return false
			}
		}
	}
	return true
}
