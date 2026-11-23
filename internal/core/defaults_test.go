package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A global default fills in only the kinds a directory leaves open.
func TestResolveWithDefaultsFillsUndeclaredKinds(t *testing.T) {
	dir := t.TempDir()
	installed := []Installed{
		{Kind: Node, Version: "20.11.1", Path: "/installed/node/20.11.1", Manager: "envmgr"},
		{Kind: Go, Version: "1.2.3", Path: "/installed/go/1.2.3", Manager: "envmgr"},
	}
	res := ResolveWithDefaults(Project{Path: dir}, installed, true, map[string]string{"node": "20.11.1"})
	if len(res) != 1 || res[0].Kind != Node || res[0].Source != "默认" {
		t.Fatalf("resolutions = %+v", res)
	}
	if res[0].Installed == nil || res[0].Installed.Version != "20.11.1" {
		t.Fatalf("installed = %+v", res[0].Installed)
	}
}

// What the project asks for always wins over the fallback.
func TestResolveWithDefaultsLosesToProjectPin(t *testing.T) {
	dir := t.TempDir()
	if err := WriteProjectConfig(dir, map[Kind]string{Node: "18.0.0"}); err != nil {
		t.Fatal(err)
	}
	installed := []Installed{
		{Kind: Node, Version: "18.0.0", Path: "/installed/node/18.0.0", Manager: "envmgr"},
		{Kind: Node, Version: "20.11.1", Path: "/installed/node/20.11.1", Manager: "envmgr"},
	}
	res := ResolveWithDefaults(Project{Path: dir}, installed, true, map[string]string{"node": "20.11.1"})
	if len(res) != 1 || res[0].Installed == nil || res[0].Installed.Version != "18.0.0" || res[0].Source != "pin" {
		t.Fatalf("resolutions = %+v", res)
	}
}

// A default whose version was removed disappears instead of nagging every
// directory on the machine.
func TestResolveWithDefaultsSkipsMissingVersion(t *testing.T) {
	dir := t.TempDir()
	installed := []Installed{{Kind: Node, Version: "20.11.1", Path: "/installed/node/20.11.1", Manager: "envmgr"}}
	res := ResolveWithDefaults(Project{Path: dir}, installed, true, map[string]string{"node": "99.0.0"})
	if len(res) != 0 {
		t.Fatalf("resolutions = %+v", res)
	}
	if got := DefaultResolutions(installed, map[string]string{"node": "99.0.0"}); len(got) != 0 {
		t.Fatalf("default resolutions = %+v", got)
	}
}

// Installs one EnvMgr-managed Node version under a throwaway HOME.
func defaultsFixture(t *testing.T, versions ...string) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	t.Setenv("HOME", home)
	for _, v := range versions {
		dir := filepath.Join(home, ".envmgr", "sdk", "node", v, "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	return svc, home
}

func TestSetDefaultStoresTheResolvedVersion(t *testing.T) {
	svc, _ := defaultsFixture(t, "18.0.0", "20.11.1")

	if err := svc.SetDefault(Node, "20"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if got := svc.Defaults()[Node]; got != "20.11.1" {
		t.Fatalf("default=%q want 20.11.1", got)
	}
	if err := svc.SetDefault(Node, "7.0.0"); err == nil {
		t.Fatal("a version that is not installed was accepted")
	}

	if err := svc.ClearDefault(Node); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}
	if len(svc.Defaults()) != 0 {
		t.Fatalf("defaults survived clearing: %+v", svc.Defaults())
	}
}

// Outside every project the default is what a terminal gets.
func TestEnvForDirUsesDefaultOutsideProject(t *testing.T) {
	svc, home := defaultsFixture(t, "20.11.1")
	if err := svc.SetDefault(Node, "20.11.1"); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(home, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}

	env := svc.EnvForDir(scratch)
	if len(env.Pairs) != 1 || env.Pairs[0].Version != "20.11.1" {
		t.Fatalf("pairs = %+v", env.Pairs)
	}
	if len(env.Path) == 0 || !strings.Contains(env.Path[0], filepath.Join("node", "20.11.1")) {
		t.Fatalf("path = %+v", env.Path)
	}
}

// Deleting the version behind a default changes every directory at once, so
// it is refused like a pinned version; --force still works.
func TestRemoveRefusesDefaultVersion(t *testing.T) {
	svc, _ := defaultsFixture(t, "20.11.1")
	if err := svc.SetDefault(Node, "20.11.1"); err != nil {
		t.Fatal(err)
	}
	inst, ok := Exact(svc.Installed(), Node, "20.11.1")
	if !ok {
		t.Fatalf("installed = %+v", svc.Installed())
	}
	err := svc.Remove(inst)
	if err == nil || !strings.Contains(err.Error(), "全局默认版本") {
		t.Fatalf("Remove error = %v", err)
	}
	if err := svc.RemoveForced(inst); err != nil {
		t.Fatalf("RemoveForced: %v", err)
	}
}
