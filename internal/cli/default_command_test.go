package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"envmgr/internal/core"
)

// The default command is the only way to make EnvMgr active outside projects,
// so it has to set, list and clear the fallback, and `env` has to see it.
func TestDefaultCommandSetsListsAndClears(t *testing.T) {
	svc, project := environmentCLI(t)
	home := filepath.Dir(project)
	sdk := filepath.Join(home, ".envmgr", "sdk", "node", "20.11.1", "bin")
	if err := os.MkdirAll(sdk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc.Refresh()

	out := captureCLIOutput(t, func() {
		if code := cmdDefault(svc, []string{"node@20"}); code != 0 {
			t.Fatalf("set exit=%d", code)
		}
	})
	if !strings.Contains(out, "20.11.1") {
		t.Fatalf("set output = %q", out)
	}
	if got := svc.Defaults()[core.Node]; got != "20.11.1" {
		t.Fatalf("default = %q", got)
	}

	// Outside the project, env now exports the fallback instead of nothing.
	scratch := filepath.Join(home, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	envOut := captureCLIOutput(t, func() {
		if code := cmdEnv(svc, []string{"--dir", scratch, "--shell", "bash"}); code != 0 {
			t.Fatalf("env exit=%d", code)
		}
	})
	if !strings.Contains(envOut, "PATH=") || !strings.Contains(envOut, "node/20.11.1") {
		t.Fatalf("env output = %q", envOut)
	}

	listOut := captureCLIOutput(t, func() {
		if code := cmdDefault(svc, nil); code != 0 {
			t.Fatalf("list exit=%d", code)
		}
	})
	if !strings.Contains(listOut, "Node.js") || !strings.Contains(listOut, "20.11.1") {
		t.Fatalf("list output = %q", listOut)
	}

	captureCLIOutput(t, func() {
		if code := cmdDefault(svc, []string{"--unset", "node"}); code != 0 {
			t.Fatalf("unset exit=%d", code)
		}
	})
	if len(svc.Defaults()) != 0 {
		t.Fatalf("defaults survived unset: %+v", svc.Defaults())
	}
}

// A version that is not installed is refused instead of silently ignored.
func TestDefaultCommandRejectsMissingVersion(t *testing.T) {
	svc, _ := environmentCLI(t)
	if code := cmdDefault(svc, []string{"node@99.0.0"}); code == 0 {
		t.Fatal("missing version accepted")
	}
}
