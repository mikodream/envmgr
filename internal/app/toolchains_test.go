package app

import (
	"fmt"
	"testing"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestToolchainViewsKeepManagersIndependentFromLanguages(t *testing.T) {
	a := newTestApp(t)
	a.page = PageToolchains
	a.installed = []core.Installed{{Kind: core.Node, Version: "24.13.0", Path: "/node"}, {Kind: core.Python, Version: "3.14.3", Path: "/python"}}
	a.inventory = buildInventoryView(a.installed)
	a.managers = []core.ManagerState{{ID: "shared", Name: "Shared manager", Desc: "Node and Python manager", Kinds: []core.Kind{core.Node, core.Python}, Installed: true}}
	tt := ui.NewTester(a.View, 1180, 780)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	assertSDK := func(version string) {
		t.Helper()
		if !tt.HasText(version) {
			t.Fatalf("SDK version %s is missing", version)
		}
		if tt.HasText("Shared manager") {
			t.Fatal("manager inventory leaked into a language's SDK list")
		}
	}
	assertSDK("24.13.0")
	click("版本管理器")
	if _, ok := tt.Find("SDK 类型"); ok {
		t.Fatal("manager view still contains language filters")
	}
	if _, ok := tt.Find("手动添加一个不在常规位置的版本"); ok {
		t.Fatal("manager view contains the SDK add action")
	}
	count := 0
	for _, text := range tt.Texts() {
		if text == "Shared manager" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("multi-language manager shown %d times, want once", count)
	}
	click("已装 SDK")
	assertSDK("24.13.0")
	click("Python")
	assertSDK("3.14.3")
	click("版本管理器")
	a.SetToolchainKind(core.Node)
	tt.Frame()
	if a.toolchainView != 0 {
		t.Fatal("SDK navigation did not return to the SDK view")
	}
	assertSDK("24.13.0")
	a.SetToolchainKind(core.Rust)
	tt.Frame()
	click("查看版本管理器")
	if a.toolchainView != 1 || !tt.HasText("Shared manager") {
		t.Fatal("empty SDK view's manager link did not open manager inventory")
	}
}

func TestToolchainManagersKeepIndependentVirtualScroll(t *testing.T) {
	a := newTestApp(t)
	a.page = PageToolchains
	a.installed = []core.Installed{{Kind: core.Node, Version: "24.13.0", Path: "/node"}}
	a.inventory = buildInventoryView(a.installed)
	a.managers = nil
	for i := 0; i < 1000; i++ {
		a.managers = append(a.managers, core.ManagerState{ID: fmt.Sprint(i), Name: fmt.Sprintf("Manager-%04d", i), Installed: true})
	}
	a.OpenManagers()
	tt := ui.NewTester(a.View, 1180, 780)
	before, ok := tt.Find("Manager-0000")
	if !ok {
		t.Fatal("first manager is missing")
	}
	burstWheel(tt, 120)
	if r, found := tt.Find("Manager-0000"); found && r.Y >= before.Y {
		t.Fatal("manager list did not scroll")
	}
	if len(tt.Texts()) > 200 {
		t.Fatal("hidden manager rows were built")
	}
	if err := tt.Click("已装 SDK"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("24.13.0") {
		t.Fatal("manager scroll offset leaked into SDK inventory")
	}
	if err := tt.Click("版本管理器"); err != nil {
		t.Fatal(err)
	}
	burstWheel(tt, -120)
	after, ok := tt.Find("Manager-0000")
	if !ok || after.Y != before.Y {
		t.Fatal("manager list did not scroll back to its top")
	}
	if tt.FramePending() {
		t.Fatal("manager wheel animation did not settle")
	}
}
