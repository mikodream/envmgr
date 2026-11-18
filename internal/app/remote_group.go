package app

import (
	"fmt"
	"sort"

	"envmgr/internal/core"
)

// remoteGroup is one version of one SDK, together with every vendor build of
// it. Java ships the same version from several vendors, so the list is a
// version × vendor matrix shown as one row per version.
type remoteGroup struct {
	Kind    core.Kind
	Version string
	LTS     bool
	Label   string
	Note    string
	// Options is one Remote per vendor, default vendor first.
	Options []core.Remote
}

// MultiVendor reports whether this version can be downloaded from more than
// one source, which is what makes the source chooser worth showing.
func (g remoteGroup) MultiVendor() bool { return len(g.Options) > 1 }

// SizeText summarises the download size: exact when every build agrees, a
// range when they differ, and empty when no vendor reports one.
func (g remoteGroup) SizeText() string {
	var min, max int64
	for _, o := range g.Options {
		if o.Size <= 0 {
			continue
		}
		if min == 0 || o.Size < min {
			min = o.Size
		}
		if o.Size > max {
			max = o.Size
		}
	}
	switch {
	case min == 0:
		return ""
	case min == max:
		return core.HumanSize(min)
	default:
		return fmt.Sprintf("%s–%s", core.HumanSize(min), core.HumanSize(max))
	}
}

// groupRemote folds a flat vendor list into one row per version, newest first.
func groupRemote(list []core.Remote) []remoteGroup {
	byKey := map[string]*remoteGroup{}
	var order []string
	for _, r := range list {
		key := string(r.Kind) + "@" + r.Version
		g, ok := byKey[key]
		if !ok {
			g = &remoteGroup{
				Kind: r.Kind, Version: r.Version, LTS: r.LTS,
				Label: r.Label, Note: r.Note,
			}
			byKey[key] = g
			order = append(order, key)
		}
		if r.LTS {
			g.LTS = true
		}
		if g.Label == "" {
			g.Label = r.Label
		}
		if g.Note == "" {
			g.Note = r.Note
		}
		g.Options = append(g.Options, r)
	}
	out := make([]remoteGroup, 0, len(order))
	for _, key := range order {
		g := byKey[key]
		// The default vendor goes first: it is what a click installs when the
		// version has only one source.
		sort.SliceStable(g.Options, func(i, j int) bool {
			a, b := g.Options[i], g.Options[j]
			if (a.Vendor == core.DefaultVendor) != (b.Vendor == core.DefaultVendor) {
				return a.Vendor == core.DefaultVendor
			}
			return a.VendorName < b.VendorName
		})
		out = append(out, *g)
	}
	versions := make(map[string]core.Version, len(out))
	for _, group := range out {
		versions[group.Version] = core.ParseVersion(group.Version)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return versions[out[i].Version].Compare(versions[out[j].Version]) > 0
	})
	return out
}

// remoteGroupedView is computed once per feed; wheel frames only select one
// of its ready lists. Feeds are immutable until replaced by a refresh.
type remoteGroupedView struct {
	source   []core.Remote
	all, lts []remoteGroup
}

func buildRemoteGroupedView(kind core.Kind, source []core.Remote) remoteGroupedView {
	var all, lts []core.Remote
	for _, remote := range source {
		if remote.Kind != kind {
			continue
		}
		all = append(all, remote)
		if remote.LTS {
			lts = append(lts, remote)
		}
	}
	return remoteGroupedView{source: source, all: groupRemote(all), lts: groupRemote(lts)}
}

func (v remoteGroupedView) matches(source []core.Remote) bool {
	return len(v.source) == len(source) && (len(source) == 0 || &v.source[0] == &source[0])
}

// groupsFor returns cached rows without filtering or sorting the full feed.
func (a *App) groupsFor(kind core.Kind) []remoteGroup {
	if a.remoteGroups == nil {
		a.remoteGroups = make(map[core.Kind]remoteGroupedView)
	}
	view, ok := a.remoteGroups[kind]
	if !ok || !view.matches(a.remote) {
		view = buildRemoteGroupedView(kind, a.remote)
		a.remoteGroups[kind] = view
	}
	if a.filterLTS {
		return view.lts
	}
	return view.all
}
