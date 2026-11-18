package app

import (
	"reflect"
	"testing"

	"envmgr/internal/core"
)

func TestInventoryViewExactMatchesCoreSemantics(t *testing.T) {
	installed := []core.Installed{
		{Kind: core.Node, Version: "v20.1", Path: "first"},
		{Kind: core.Node, Version: "20.1.0", Path: "second"},
		{Kind: core.Node, Version: "20.1.0-rc.1", Path: "preview"},
		{Kind: core.Java, Version: "20.1", Path: "java"},
	}
	view := buildInventoryView(installed)
	for _, kind := range []core.Kind{core.Node, core.Java, core.Python} {
		for _, version := range []string{"20.1", "V20.01.0.0", "20.1.0-rc.1", "20.1.0-rc.2", "20.2", "garbage", "0.0"} {
			want, wantOK := core.Exact(installed, kind, version)
			got, gotOK := view.exact(kind, version)
			if gotOK != wantOK || got.Path != want.Path {
				t.Fatalf("exact(%s,%s)=(%+v,%v), want (%+v,%v)", kind, version, got, gotOK, want, wantOK)
			}
		}
	}
}

func TestInventoryViewNewestFirstMixedKinds(t *testing.T) {
	installed := []core.Installed{
		{Kind: core.Node, Version: "v20.9.0", Path: "node-old"},
		{Kind: core.Python, Version: "3.9.9", Path: "python-old"},
		{Kind: core.Node, Version: "20.10.0-rc.1", Path: "node-rc"},
		{Kind: core.Java, Version: "21", Path: "java-first"},
		{Kind: core.Node, Version: "20.10.0", Path: "node-new"},
		{Kind: core.Python, Version: "3.12.0", Path: "python-new"},
		{Kind: core.Java, Version: "21.0.0", Path: "java-second"},
		{Kind: core.Node, Version: "v20.10.0", Path: "node-equal"},
	}
	original := append([]core.Installed(nil), installed...)
	view := buildInventoryView(installed)
	for _, tc := range []struct {
		kind core.Kind
		want []string
	}{
		{core.Node, []string{"node-new", "node-equal", "node-rc", "node-old"}},
		{core.Python, []string{"python-new", "python-old"}},
		{core.Java, []string{"java-first", "java-second"}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			var got []string
			for _, item := range view.list(tc.kind) {
				if item.Kind != tc.kind {
					t.Errorf("kind = %q, want %q", item.Kind, tc.kind)
				}
				got = append(got, item.Path)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("paths = %v, want %v", got, tc.want)
			}
		})
	}
	if !reflect.DeepEqual(installed, original) {
		t.Fatal("building inventory view changed the input slice")
	}
	installed[4].Version = "99.0.0"
	if got := view.list(core.Node)[0].Version; got != "20.10.0" {
		t.Errorf("cached version = %q after input changed", got)
	}
}

func TestInventoryViewEmpty(t *testing.T) {
	for _, view := range []inventoryView{{}, buildInventoryView(nil)} {
		if got := view.list(core.Go); len(got) != 0 {
			t.Errorf("empty inventory returned %v", got)
		}
	}
}

func TestDoctorViewSevereFirstWithoutMutation(t *testing.T) {
	report := core.Report{Sections: []core.Section{
		{Title: "healthy", Checks: []core.Check{{Title: "ok", Level: core.LevelOK}}},
		{Title: "warning", Checks: []core.Check{{Title: "warn", Level: core.LevelWarn}}},
		{Title: "failure", Desc: "preserve description", Checks: []core.Check{
			{Title: "info", Level: core.LevelInfo},
			{Title: "ok", Level: core.LevelOK},
			{Title: "warn-first", Level: core.LevelWarn},
			{Title: "fail-first", Level: core.LevelFail, Detail: "detail", Hint: "hint", Fix: core.FixRefresh},
			{Title: "warn-second", Level: core.LevelWarn},
			{Title: "fail-second", Level: core.LevelFail},
		}},
		{Title: "failure-second", Checks: []core.Check{{Title: "fail", Level: core.LevelFail}}},
		{Title: "informational", Checks: []core.Check{{Title: "info", Level: core.LevelInfo}}},
	}}
	original := report
	original.Sections = append([]core.Section(nil), report.Sections...)
	for i := range original.Sections {
		original.Sections[i].Checks = append([]core.Check(nil), report.Sections[i].Checks...)
	}
	view := buildDoctorView(report)
	var titles []string
	for _, section := range view.Sections {
		titles = append(titles, section.Title)
	}
	wantSections := []string{"failure", "failure-second", "warning", "healthy", "informational"}
	if !reflect.DeepEqual(titles, wantSections) {
		t.Errorf("sections = %v, want %v", titles, wantSections)
	}
	var checks []string
	for _, check := range view.Sections[0].Checks {
		checks = append(checks, check.Title)
	}
	wantChecks := []string{"fail-first", "fail-second", "warn-first", "warn-second", "ok", "info"}
	if !reflect.DeepEqual(checks, wantChecks) {
		t.Errorf("checks = %v, want %v", checks, wantChecks)
	}
	if got := view.Sections[0]; got.Desc != "preserve description" || !reflect.DeepEqual(got.Checks[0], original.Sections[2].Checks[3]) {
		t.Error("sorting changed section or check metadata")
	}
	if !reflect.DeepEqual(report, original) {
		t.Fatal("building doctor view changed the report")
	}
	report.Sections[2].Checks[3].Title = "changed"
	report.Sections[2].Title = "changed"
	if view.Sections[0].Title != "failure" || view.Sections[0].Checks[0].Title != "fail-first" {
		t.Error("cached sections or checks share input slice storage")
	}
}

func TestDoctorViewEmpty(t *testing.T) {
	if got := buildDoctorView(core.Report{}); len(got.Sections) != 0 {
		t.Errorf("empty report returned %v", got.Sections)
	}
}
