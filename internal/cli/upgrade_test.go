package cli

import (
	"strings"
	"testing"

	"envmgr/internal/core"
)

// pad measures display cells, so a column of Chinese labels lines up with the
// ASCII ones next to it.
func TestPadCountsWideRunes(t *testing.T) {
	// "更新" is four cells, so ten leaves six spaces.
	if got := pad("更新", 10); got != "更新      " {
		t.Fatalf("pad(更新,10) = %q", got)
	}
	if !strings.HasPrefix(pad("Node.js", 9), "Node.js  ") {
		t.Fatalf("pad(Node.js,9) = %q", pad("Node.js", 9))
	}
	if got := pad("toolong", 3); got != "toolong" {
		t.Fatalf("pad truncated a value: %q", got)
	}
}

func TestLevelAndEOLLabels(t *testing.T) {
	u := core.Update{Version: "20.11.1", Latest: "20.19.0", Level: core.MinorUpdate,
		EOL: "2027-04-30", EOLState: core.EOLActive}
	if got := levelLabel(u); got != "小版本更新" {
		t.Errorf("levelLabel = %q", got)
	}
	if got := eolLabel(u); got != "2027-04-30 结束" {
		t.Errorf("eolLabel = %q", got)
	}
	u.Latest, u.Version, u.Level = "20.11.1", "20.11.1", core.UpToDate
	if got := levelLabel(u); got != "最新" {
		t.Errorf("up-to-date label = %q", got)
	}
	u.EOL, u.EOLState, u.DaysLeft = "2026-01-01", core.EOLEnded, -300
	if got := eolLabel(u); got != "2026-01-01 已停止" {
		t.Errorf("ended label = %q", got)
	}
	u.EOLState, u.DaysLeft = core.EOLSoon, 30
	if got := eolLabel(u); got != "30 天后停止" {
		t.Errorf("soon label = %q", got)
	}
	u.EOLState = core.EOLUnknown
	if got := eolLabel(u); got != "—" {
		t.Errorf("unknown label = %q", got)
	}
}

// With no arguments, upgrade takes exactly the outdated versions; the caller
// then filters by manager so another tool's installs are never touched.
func TestUpgradeTargetsWithNoArgsTakesOutdatedOnly(t *testing.T) {
	updates := []core.Update{
		{Kind: core.Node, Version: "20.11.1", Latest: "20.19.0", Level: core.MinorUpdate, Manager: "envmgr"},
		{Kind: core.Node, Version: "22.1.0", Latest: "22.1.0", Level: core.UpToDate, Manager: "envmgr"},
		{Kind: core.Python, Version: "3.12.4", Latest: "3.12.7", Level: core.PatchUpdate, Manager: "uv"},
	}
	got, code := upgradeTargets(nil, updates, nil)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(got) != 2 {
		t.Fatalf("targets = %+v", got)
	}
	if got[0].Version != "20.11.1" || got[1].Version != "3.12.4" {
		t.Fatalf("targets = %+v", got)
	}
}

func TestUpgradeTargetsRejectsBadSpec(t *testing.T) {
	_, code := upgradeTargets(nil, nil, []string{"node"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
