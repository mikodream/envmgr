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
	sort.SliceStable(out, func(i, j int) bool {
		return core.ParseVersion(out[i].Version).Compare(core.ParseVersion(out[j].Version)) > 0
	})
	return out
}

// groupsFor returns the grouped rows of one kind, filtered by the LTS switch.
func (a *App) groupsFor(kind core.Kind) []remoteGroup {
	groups := groupRemote(a.visibleRemote(kind))
	if !a.filterLTS {
		return groups
	}
	out := groups[:0]
	for _, g := range groups {
		if g.LTS {
			out = append(out, g)
		}
	}
	return out
}
