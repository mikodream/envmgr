package app

import (
	"sort"
	"strconv"
	"strings"

	"envmgr/internal/core"
)

// inventoryView holds the display order computed when inventory is assigned.
// Callers treat its lists as read-only until the next inventory assignment.
type inventoryView struct {
	byKind      map[core.Kind][]core.Installed
	exactByKind map[core.Kind]map[string]core.Installed
}

func buildInventoryView(installed []core.Installed) inventoryView {
	type entry struct {
		installed core.Installed
		version   core.Version
	}
	grouped := make(map[core.Kind][]entry)
	view := inventoryView{byKind: make(map[core.Kind][]core.Installed), exactByKind: make(map[core.Kind]map[string]core.Installed)}
	for _, item := range installed {
		version := core.ParseVersion(item.Version)
		grouped[item.Kind] = append(grouped[item.Kind], entry{installed: item, version: version})
		if view.exactByKind[item.Kind] == nil {
			view.exactByKind[item.Kind] = make(map[string]core.Installed)
		}
		key := normalizedVersionKey(version)
		if _, exists := view.exactByKind[item.Kind][key]; !exists {
			view.exactByKind[item.Kind][key] = item
		}
	}
	for kind, entries := range grouped {
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].version.Compare(entries[j].version) > 0
		})
		list := make([]core.Installed, len(entries))
		for i, entry := range entries {
			list[i] = entry.installed
		}
		view.byKind[kind] = list
	}
	return view
}

func (v inventoryView) list(kind core.Kind) []core.Installed {
	return v.byKind[kind]
}

// exact preserves core.Exact's semantic matching and first-source preference,
// with one version parse and a map lookup rather than an inventory scan per row.
func (v inventoryView) exact(kind core.Kind, version string) (core.Installed, bool) {
	item, ok := v.exactByKind[kind][normalizedVersionKey(core.ParseVersion(version))]
	return item, ok
}

func normalizedVersionKey(version core.Version) string {
	parts := version.Parts
	for len(parts) > 0 && parts[len(parts)-1] == 0 {
		parts = parts[:len(parts)-1]
	}
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(part))
		key.WriteByte('.')
	}
	key.WriteByte('|')
	key.WriteString(version.Pre)
	return key.String()
}

// doctorView holds sorted copies of a report's sections and checks.
type doctorView struct {
	Sections    []core.Section
	WorstCounts []int
}

func buildDoctorView(report core.Report) doctorView {
	sections := sortedSections(report.Sections)
	counts := make([]int, len(sections))
	for i := range sections {
		sections[i].Checks = sortedChecks(sections[i].Checks)
		counts[i] = worstCount(sections[i])
	}
	return doctorView{Sections: sections, WorstCounts: counts}
}
