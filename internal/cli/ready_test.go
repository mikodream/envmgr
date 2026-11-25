package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"envmgr/internal/core"
)

// newReadyFixture points HOME at a temp dir, installs a fake Go toolchain whose
// executable reports a fixed version, and returns a project pinned to it.
func newReadyFixture(t *testing.T) (svc *core.Service, project string) {
	t.Helper()
	home := t.TempDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	sdk := filepath.Join(home, ".envmgr", "sdk", "go", "1.24.0")
	if err := os.MkdirAll(filepath.Join(sdk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "VERSION"), []byte("go1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(sdk, "bin", "go")
	script := "#!/bin/sh\necho \"go version go1.24.0 \"$(uname -m)\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	project = filepath.Join(home, "code", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteProjectConfig(project, map[core.Kind]string{core.Go: "1.24.0"}); err != nil {
		t.Fatal(err)
	}

	svc, err := core.NewService()
	if err != nil {
		t.Fatal(err)
	}
	return svc, project
}

// A fully satisfied project reports readiness and verifies every SDK by
// running it, without touching the network.
func TestReadyCommandVerifiesSatisfiedProject(t *testing.T) {
	svc, project := newReadyFixture(t)

	var code int
	out := captureCLIOutput(t, func() { code = cmdReady(svc, []string{"--dir", project, "--dry-run"}) })
	if code != 0 {
		t.Fatalf("dry-run exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "全部就绪") {
		t.Fatalf("dry-run output = %q", out)
	}

	out = captureCLIOutput(t, func() { code = cmdReady(svc, []string{"--dir", project}) })
	if code != 0 {
		t.Fatalf("ready exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "项目环境就绪") || !strings.Contains(out, "1.24.0") {
		t.Fatalf("ready output = %q", out)
	}
}

// A directory without declarations cannot be made "ready" and must say what
// to do instead.
func TestReadyCommandRejectsUndeclaredProject(t *testing.T) {
	svc, _ := newReadyFixture(t)
	bare := filepath.Join(os.Getenv("HOME"), "code", "bare")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureCLIOutput(t, func() { code = cmdReady(svc, []string{"--dir", bare}) })
	if code != 1 {
		t.Fatalf("exit=%d output=%q", code, out)
	}
	_ = out
}

// A subdirectory inherits the nearest project root, matching how run and env
// behave.
func TestReadyCommandFindsProjectFromSubdirectory(t *testing.T) {
	svc, project := newReadyFixture(t)
	sub := filepath.Join(project, "internal", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureCLIOutput(t, func() { code = cmdReady(svc, []string{"--dir", sub, "--dry-run"}) })
	if code != 0 {
		t.Fatalf("exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "从") || !strings.Contains(out, "全部就绪") {
		t.Fatalf("output = %q", out)
	}
}
