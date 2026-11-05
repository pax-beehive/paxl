package adaptor

import (
	"sort"

	"github.com/pax-oss/paxl/internal/model"
)

func (r *configurationReader) finishConfiguration() {
	r.recordConfigurationConflicts()
	if !r.req.IncludeSensitive {
		r.redactConfiguration()
	}
	seen := make(map[string]bool)
	sources := make([]model.ConfigurationSource, 0, len(r.result.Sources))
	for _, source := range r.result.Sources {
		normalizeConfigurationSource(&source)
		key := configurationSourceKey(&source)
		if !seen[key] {
			seen[key] = true
			sources = append(sources, source)
		}
	}
	r.result.Sources = sources
	sort.SliceStable(r.result.Skills, func(i, j int) bool {
		a, b := r.result.Skills[i], r.result.Skills[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return configurationSourceKey(&a.Source) < configurationSourceKey(&b.Source)
	})
	sort.SliceStable(r.result.MCPServers, func(i, j int) bool {
		a, b := r.result.MCPServers[i], r.result.MCPServers[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return configurationSourceKey(&a.Source) < configurationSourceKey(&b.Source)
	})
	sort.SliceStable(r.result.Sources, func(i, j int) bool {
		return configurationSourceKey(
			&r.result.Sources[i],
		) < configurationSourceKey(
			&r.result.Sources[j],
		)
	})
}

func configurationSourceKey(source *model.ConfigurationSource) string {
	return source.Path + "\x00" + source.Pointer + "\x00" + string(
		source.Scope,
	) + "\x00" + source.Format
}

func (r *configurationReader) recordConfigurationConflicts() {
	groups := map[model.AgentConfigurationKind]map[string][]model.ConfigurationSource{
		model.AgentConfigurationKindSkills: {}, model.AgentConfigurationKindMCP: {},
	}
	for _, skill := range r.result.Skills {
		groups[model.AgentConfigurationKindSkills][skill.Name] = append(
			groups[model.AgentConfigurationKindSkills][skill.Name],
			skill.Source,
		)
	}
	for _, server := range r.result.MCPServers {
		groups[model.AgentConfigurationKindMCP][server.Name] = append(
			groups[model.AgentConfigurationKindMCP][server.Name],
			server.Source,
		)
	}
	for kind, names := range groups {
		for name, candidates := range names {
			if len(candidates) < 2 {
				continue
			}
			sort.SliceStable(candidates, func(i, j int) bool {
				return configurationSourceKey(
					&candidates[i],
				) < configurationSourceKey(
					&candidates[j],
				)
			})
			r.result.Conflicts = append(
				r.result.Conflicts,
				model.ConfigurationConflict{
					Kind:       kind,
					Name:       name,
					Candidates: candidates,
					Resolution: "unknown",
				},
			)
		}
	}
	sort.Slice(r.result.Conflicts, func(i, j int) bool {
		a, b := r.result.Conflicts[i], r.result.Conflicts[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
}
