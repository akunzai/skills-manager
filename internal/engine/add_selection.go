package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type AddSelectionKind uint8

const (
	AddSelectionResolved AddSelectionKind = iota
	AddSelectionNeedsSkills
	AddSelectionNeedsPath
	AddSelectionCancelled
)

type AddSelectionCancelReason uint8

const (
	AddSelectionNotCancelled AddSelectionCancelReason = iota
	AddSelectionUserCancelled
	AddSelectionEmpty
)

type AddSelectionRequest struct {
	All    bool
	Skills []string
}

type AddSelectionAnswers struct {
	Skills       []string
	Paths        map[string]string
	CancelReason AddSelectionCancelReason
}

type AddSelectionOutcome struct {
	Kind         AddSelectionKind
	Skill        string
	Options      []string
	Skills       map[string]string
	CancelReason AddSelectionCancelReason
}

func ResolveAddSelection(discovered DiscoveredSkills, request AddSelectionRequest, answers AddSelectionAnswers) (AddSelectionOutcome, error) {
	if request.All && len(request.Skills) > 0 {
		return AddSelectionOutcome{}, fmt.Errorf("--all cannot be combined with named Skills")
	}
	if len(discovered) == 0 {
		return AddSelectionOutcome{}, fmt.Errorf("no Skills discovered")
	}
	if answers.CancelReason != AddSelectionNotCancelled {
		return AddSelectionOutcome{Kind: AddSelectionCancelled, CancelReason: answers.CancelReason}, nil
	}

	selected, outcome, err := selectedSkillNames(discovered, request, answers)
	if err != nil || outcome.Kind != AddSelectionResolved {
		return outcome, err
	}

	resolved := make(map[string]string, len(selected))
	for _, name := range selected {
		paths := discovered[name]
		if len(paths) == 0 {
			return AddSelectionOutcome{}, fmt.Errorf("no Source path found for Skill %q", name)
		}
		if len(paths) == 1 {
			resolved[name] = paths[0]
			continue
		}
		path := answers.Paths[name]
		if path == "" {
			return AddSelectionOutcome{Kind: AddSelectionNeedsPath, Skill: name, Options: slices.Clone(paths)}, nil
		}
		if !slices.Contains(paths, path) {
			return AddSelectionOutcome{}, fmt.Errorf("Source path %q is not a candidate for Skill %q", path, name)
		}
		resolved[name] = path
	}
	return AddSelectionOutcome{Kind: AddSelectionResolved, Skills: resolved}, nil
}

func selectedSkillNames(discovered DiscoveredSkills, request AddSelectionRequest, answers AddSelectionAnswers) ([]string, AddSelectionOutcome, error) {
	if request.All {
		return slices.Sorted(maps.Keys(discovered)), AddSelectionOutcome{}, nil
	}
	if len(request.Skills) > 0 {
		selected, missing := matchDiscoveredSkills(discovered, request.Skills)
		if len(missing) > 0 {
			return nil, AddSelectionOutcome{}, fmt.Errorf("Skills not found in discovered list: %s", strings.Join(missing, ", "))
		}
		return selected, AddSelectionOutcome{}, nil
	}
	if answers.Skills != nil {
		if len(answers.Skills) == 0 {
			return nil, AddSelectionOutcome{Kind: AddSelectionCancelled, CancelReason: AddSelectionEmpty}, nil
		}
		selected, missing := matchDiscoveredSkills(discovered, answers.Skills)
		if len(missing) > 0 {
			return nil, AddSelectionOutcome{}, fmt.Errorf("selected Skills not found in discovered list: %s", strings.Join(missing, ", "))
		}
		return selected, AddSelectionOutcome{}, nil
	}
	if len(discovered) == 1 {
		return slices.Sorted(maps.Keys(discovered)), AddSelectionOutcome{}, nil
	}
	options := slices.Sorted(maps.Keys(discovered))
	return nil, AddSelectionOutcome{Kind: AddSelectionNeedsSkills, Options: options}, nil
}

func matchDiscoveredSkills(discovered DiscoveredSkills, requested []string) ([]string, []string) {
	selected := make(map[string]struct{}, len(requested))
	var missing []string
	for _, requestedName := range requested {
		if _, ok := discovered[requestedName]; ok {
			selected[requestedName] = struct{}{}
			continue
		}
		var matches []string
		for discoveredName := range maps.Keys(discovered) {
			if strings.EqualFold(discoveredName, requestedName) {
				matches = append(matches, discoveredName)
			}
		}
		if len(matches) != 1 {
			missing = append(missing, requestedName)
			continue
		}
		selected[matches[0]] = struct{}{}
	}
	return slices.Sorted(maps.Keys(selected)), missing
}
