package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"envmgr/internal/core"
)

func environmentCLI(t *testing.T) (*core.Service, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	svc, err := core.NewService()
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func TestRunSeparatorPreservesChildDirectoryFlag(t *testing.T) {
	args := []string{"--dir", "project", "--", "tool", "--dir", "output", "--dir=other"}
	dir, rest := flagValue(args, "dir", "")
	if dir != "project" || !reflect.DeepEqual(rest, args[2:]) {
		t.Fatalf("directory=%q, child=%v", dir, rest)
	}
	// Execute this test binary as a real child; its --dir must reach it untouched.
	svc, project := environmentCLI(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENVMGR_CLI_CHILD", "1")
	if code := cmdRun(svc, []string{"--dir", project, "--", exe, "-test.run=^TestRunChildArgumentHelper$", "--", "--dir", "output"}); code != 0 {
		t.Fatalf("child exit=%d", code)
	}
}

func TestRunChildArgumentHelper(t *testing.T) {
	if os.Getenv("ENVMGR_CLI_CHILD") != "1" {
		return
	}
	n := len(os.Args)
	if n < 3 || !reflect.DeepEqual(os.Args[n-3:], []string{"--", "--dir", "output"}) {
		os.Exit(9)
	}
}

func TestUseMergesProjectFileAndUpdatesRegisteredEnvironment(t *testing.T) {
	svc, dir := environmentCLI(t)
	for _, spec := range []string{"node@99.1.0", "python@99.2.0"} {
		if code := cmdUse(svc, []string{"--dir", dir, spec}); code != 0 {
			t.Fatalf("use exit=%d", code)
		}
	}
	pins, err := core.ReadProjectPins(dir)
	if err != nil || len(pins) != 2 || pins[core.Node] != "99.1.0" {
		t.Fatalf("pins=%v, err=%v", pins, err)
	}
	if _, err := svc.AddProject(dir); err != nil {
		t.Fatal(err)
	}
	if code := cmdUse(svc, []string{"--dir", dir, "node@99.3.0"}); code != 0 {
		t.Fatal(code)
	}
	// Resolve through a fresh CLI process view, and the already-running GUI view.
	for _, service := range []*core.Service{svc, func() *core.Service {
		s, e := core.NewService()
		if e != nil {
			t.Fatal(e)
		}
		return s
	}()} {
		env := service.EnvForDir(dir)
		if err := env.Validate(); err == nil || !strings.Contains(err.Error(), "99.3.0") {
			t.Fatalf("expected latest missing pin: %v", err)
		}
	}
}

func TestMissingProjectSDKBlocksRunAndEnvironmentExport(t *testing.T) {
	svc, dir := environmentCLI(t)
	if err := core.WriteProjectConfig(dir, map[core.Kind]string{core.Node: "99999.0.0"}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if code := cmdRun(svc, []string{"--dir", dir, "--", exe, "-test.run=^$"}); code == 0 {
		t.Fatal("run silently launched without pinned SDK")
	}
	output := captureCLIOutput(t, func() {
		if code := cmdEnv(svc, []string{"--dir", dir, "--shell", "bash"}); code == 0 {
			t.Fatal("export succeeded with missing SDK")
		}
	})
	if output != "" {
		t.Fatalf("partial activation script: %q", output)
	}
}

func TestDoctorReportsBrokenConfigurationWithoutPanic(t *testing.T) {
	svc, dir := environmentCLI(t)
	if err := os.WriteFile(filepath.Join(dir, core.ConfigFileName), []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := doctorDir(svc, dir); code == 0 {
		t.Fatal("doctor accepted broken config")
	}
	if _, err := svc.AddProject(dir); err != nil {
		t.Fatal(err)
	}
	for _, section := range core.Diagnose(svc).Sections {
		for _, check := range section.Checks {
			if check.Title == filepath.Base(dir) && check.Level == core.LevelFail && strings.Contains(check.Detail, "配置错误") {
				return
			}
		}
	}
	t.Fatal("doctor did not report project configuration error")
}

// One missing SDK must not erase the SDKs that do resolve, and a subdirectory
// must inherit the same environment as its project root.
func TestResolvedSDKsExportWhenAnotherKindMissing(t *testing.T) {
	svc, dir := environmentCLI(t)
	home := filepath.Dir(dir)
	sdk := filepath.Join(home, "manual", "go", "1.2.3")
	if err := os.MkdirAll(filepath.Join(sdk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "bin", "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddManualInstall(core.Go, "1.2.3", sdk, ""); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteProjectConfig(dir, map[core.Kind]string{
		core.Go:   "1.2.3",
		core.Node: "99999.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "src", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{dir, sub} {
		code := 0
		out := captureCLIOutput(t, func() {
			code = cmdEnv(svc, []string{"--dir", target, "--shell", "bash"})
		})
		if code != 0 {
			t.Fatalf("export exit=%d for %s", code, target)
		}
		if !strings.Contains(out, "GOROOT=") || !strings.Contains(out, "export PATH=") {
			t.Fatalf("resolved SDK missing from export for %s: %q", target, out)
		}
	}
}
