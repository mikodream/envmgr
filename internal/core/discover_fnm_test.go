package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fnmTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("FNM_DIR", "")
	return home
}

func fnmTestNode(t *testing.T, dataDir, version string, wrapped bool) string {
	t.Helper()
	root := filepath.Join(dataDir, "node-versions", version)
	if wrapped {
		root = filepath.Join(root, "installation")
	}
	bin := root
	if runtime.GOOS != "windows" {
		bin = filepath.Join(root, "bin")
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, ExecName("node")), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestFNMDataDir(t *testing.T) {
	home := fnmTestHome(t)
	defaultDir := filepath.Join(xdgData(), "fnm")
	if runtime.GOOS == "darwin" {
		defaultDir = filepath.Join(appSupport(), "fnm")
	}
	if got := fnmDataDir(); got != defaultDir {
		t.Fatalf("default = %q, want %q", got, defaultDir)
	}

	xdgDir := filepath.Join(xdgData(), "fnm")
	if err := os.MkdirAll(xdgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := fnmDataDir(); got != xdgDir {
		t.Fatalf("existing XDG directory = %q, want %q", got, xdgDir)
	}

	// FNM_DIR takes precedence even before it has been created.
	t.Setenv("FNM_DIR", "~/custom-fnm")
	if got, want := fnmDataDir(), filepath.Join(home, "custom-fnm"); got != want {
		t.Fatalf("configured directory = %q, want %q", got, want)
	}
}

func TestFNMNodeInstalls(t *testing.T) {
	home := fnmTestHome(t)
	custom := filepath.Join(home, "custom-fnm")
	t.Setenv("FNM_DIR", custom)
	want := map[string]string{
		"22.1.0": fnmTestNode(t, custom, "v22.1.0", true),
		"24.8.0": fnmTestNode(t, filepath.Join(xdgData(), "fnm"), "v24.8.0", true),
		"20.0.0": fnmTestNode(t, custom, "v20.0.0", false),
	}
	if runtime.GOOS == "darwin" {
		want["24.14.0"] = fnmTestNode(t, filepath.Join(appSupport(), "fnm"), "v24.14.0", true)
	}
	// Incomplete and hidden downloads must not appear as installations.
	if err := os.MkdirAll(filepath.Join(custom, "node-versions", "v25.0.0", "installation"), 0o755); err != nil {
		t.Fatal(err)
	}
	fnmTestNode(t, custom, ".v26.0.0", true)

	got := fnmNodeInstalls()
	if len(got) != len(want) {
		t.Fatalf("installs = %+v, want %d", got, len(want))
	}
	for _, inst := range got {
		if inst.Path != want[inst.Version] || inst.Manager != "fnm" || inst.Kind != Node {
			t.Errorf("unexpected install: %+v", inst)
		}
		if inst.ExePath() == "" {
			t.Errorf("missing executable for %+v", inst)
		}
	}
}

func TestFNMVersionPrefersExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	fnmTestHome(t)
	root := fnmTestNode(t, fnmDataDir(), "v20.0.0", true)
	if err := os.WriteFile(filepath.Join(root, "bin", "node"), []byte("#!/bin/sh\nprintf 'v22.1.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := fnmNodeInstalls()
	if len(got) != 1 || got[0].Version != "22.1.0" {
		t.Fatalf("installs = %+v, want executable version 22.1.0", got)
	}
}

func TestFNMDataDirsDeduplicate(t *testing.T) {
	fnmTestHome(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(homeDir(), "data"))
	t.Setenv("FNM_DIR", filepath.Join(xdgData(), "fnm"))
	seen := map[string]bool{}
	for _, dir := range fnmDataDirs() {
		if seen[dir] {
			t.Fatalf("duplicate data directory %q", dir)
		}
		seen[dir] = true
	}
	root := fnmTestNode(t, fnmDataDir(), "v22.1.0", true)
	got := fnmNodeInstalls()
	if len(got) != 1 || got[0].Path != root {
		t.Fatalf("installs = %+v, want one install at %q", got, root)
	}
}
