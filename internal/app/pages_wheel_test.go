package app

import (
	"fmt"
	"testing"
	"time"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestOtherPagesHandleRepeatedWheelBursts(t *testing.T) {
	a := newTestApp(t)
	home := t.TempDir()
	a.projects = nil
	a.resolutions = map[string][]core.Resolution{}
	for i := 0; i < 10; i++ {
		p := core.NewProject(home)
		p.ID = fmt.Sprint(i)
		p.Name = fmt.Sprintf("Project-%02d", i)
		a.projects = append(a.projects, p)
	}
	a.installed = nil
	for i := 1; i <= 1000; i++ {
		a.installed = append(a.installed, core.Installed{Kind: core.Node, Version: fmt.Sprintf("%d.0.0", i), Path: fmt.Sprintf("/sdk/node/%d", i), Manager: "envmgr"})
	}
	a.inventory = buildInventoryView(a.installed)
	a.report = core.Report{}
	for section := 0; section < 6; section++ {
		sec := core.Section{Title: fmt.Sprintf("检测组-%d", section)}
		for i := 0; i < 100; i++ {
			sec.Checks = append(sec.Checks, core.Check{Title: fmt.Sprintf("检查-%d-%d", section, i), Detail: "cached result", Level: core.LevelOK})
			a.report.Total++
			a.report.OK++
		}
		a.report.Sections = append(a.report.Sections, sec)
	}
	a.doctor = buildDoctorView(a.report)
	var remotes []core.Remote
	for i := 1; i <= 5000; i++ {
		remotes = append(remotes, core.Remote{Kind: core.Node, Version: fmt.Sprintf("%d.0.0", i), LTS: i%2 == 0})
	}
	a.SetRemote(core.Node, remotes)
	a.toolchainKind = kindIndexOf(core.Node)
	a.downloadKind = kindIndexOf(core.Node)
	a.filterLTS = false
	cases := []struct {
		page    int
		anchor  string
		virtual bool
	}{
		{PageDashboard, "项目的 SDK 版本，一处管好", false},
		{PageToolchains, "1000.0.0", true},
		{PageDownloads, "5000.0.0", true},
		{PageDoctor, "检测组-0", true},
		{PageSettings, "外观", false},
	}
	for _, tc := range cases {
		t.Run(pageTitles[tc.page].Title, func(t *testing.T) {
			a.page = tc.page
			tt := ui.NewTester(a.View, 1180, 560)
			before, ok := tt.Find(tc.anchor)
			if !ok {
				t.Fatalf("missing initial anchor %q", tc.anchor)
			}
			start := time.Now()
			burstWheel(tt, 120)
			down, found := tt.Find(tc.anchor)
			if found && down.Y >= before.Y {
				t.Fatalf("page did not scroll down: before=%+v after=%+v", before, down)
			}
			if tc.virtual && len(tt.Texts()) > 250 {
				t.Fatalf("list built %d text nodes for hidden rows", len(tt.Texts()))
			}
			burstWheel(tt, -120)
			after, ok := tt.Find(tc.anchor)
			if !ok || after.Y != before.Y {
				t.Fatalf("page did not return to top: before=%+v after=%+v found=%v", before, after, ok)
			}
			if tt.FramePending() {
				t.Fatal("wheel animation did not settle")
			}
			t.Logf("60 wheel events settled in %s (headless elapsed)", time.Since(start))
		})
	}
}

func TestSettingsWheelUsesCachedEnvironment(t *testing.T) {
	a := newTestApp(t)
	a.page = PageSettings
	a.settings = settingsSnapshot{Ready: true, Shell: core.ShellPowerShell, HookFiles: []string{"cached-profile"}, HookInstalled: true, HookInstalledIn: map[string]bool{"cached-profile": true}, GitRulesInstalled: true, GitRemoteRulesSupported: true, HookScript: "cached hook script"}
	// A fresh probe would fail or read a different value. Cached settings remain
	// visible until an explicit background update, throughout actual wheel frames.
	t.Setenv("PATH", t.TempDir())
	tt := ui.NewTester(a.View, 1180, 560)
	for i := 0; i < 3; i++ {
		burstWheel(tt, 120)
		burstWheel(tt, -120)
	}
	if !tt.HasText("✓ cached-profile") {
		t.Fatal("hook status was read again instead of using its snapshot")
	}
	if !tt.HasText("自动套用中") {
		t.Fatal("Git rule status was read again instead of using its snapshot")
	}
	if tt.HasText("当前 git 版本低于") {
		t.Fatal("render probed the unavailable Git executable instead of using its snapshot")
	}
	if tt.FramePending() {
		t.Fatal("settings wheel animation did not settle")
	}
}

func TestGroupedFeedCacheReusesRowsAndRefreshes(t *testing.T) {
	a := &App{}
	a.remote = []core.Remote{{Kind: core.Java, Version: "21.0.1", LTS: true}, {Kind: core.Java, Version: "27.0.1"}}
	first := a.groupsFor(core.Java)
	for i := 0; i < 100; i++ {
		next := a.groupsFor(core.Java)
		if &first[0] != &next[0] {
			t.Fatal("unchanged feed rebuilt grouped rows")
		}
	}
	a.filterLTS = true
	if rows := a.groupsFor(core.Java); len(rows) != 1 || rows[0].Version != "21.0.1" {
		t.Fatalf("LTS cache = %+v", rows)
	}
	a.filterLTS = false
	if &a.groupsFor(core.Java)[0] != &first[0] {
		t.Fatal("LTS switch discarded the full feed cache")
	}
	// A feed refresh can replace entries in a reused backing slice. Explicit
	// publication must rebuild caches even when the source address stays equal.
	a.remote[0].Version = "21.0.2"
	a.cacheRemote(core.Java, a.remote)
	rows := a.groupsFor(core.Java)
	if rows[1].Version != "21.0.2" {
		t.Fatalf("refresh retained stale groups: %+v", rows)
	}
}
