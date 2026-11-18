package app

import (
	"reflect"
	"testing"

	"envmgr/internal/core"
)

func fakeSettingsProbes() settingsProbes {
	return settingsProbes{
		currentShell:            func() core.Shell { return core.ShellPowerShell },
		hookFiles:               func(core.Shell) []string { return nil },
		hookInstalledIn:         func(string) bool { return false },
		gitRulesInstalled:       func() bool { return false },
		gitRemoteRulesSupported: func() bool { return false },
		profiles:                func() []core.GitProfile { return nil },
	}
}

func TestSettingsSnapshotCachesGitSupportUntilReload(t *testing.T) {
	probes := fakeSettingsProbes()
	calls := 0
	supported := true
	probes.gitRemoteRulesSupported = func() bool {
		calls++
		return supported
	}

	snapshot := readSettingsSnapshot(probes)
	supported = false
	for frame := 0; frame < 100; frame++ {
		if !snapshot.Ready || !snapshot.GitRemoteRulesSupported {
			t.Fatal("cached settings changed before a refresh")
		}
	}
	if calls != 1 {
		t.Fatalf("Git support probes after reading cached settings = %d, want 1", calls)
	}

	refreshed := readSettingsSnapshot(probes)
	if !refreshed.Ready || refreshed.GitRemoteRulesSupported {
		t.Fatal("refresh did not pick up changed Git support")
	}
	if calls != 2 {
		t.Fatalf("Git support probes after explicit refresh = %d, want 2", calls)
	}
	if !snapshot.GitRemoteRulesSupported {
		t.Fatal("refresh changed the previously loaded snapshot")
	}
}

func TestSettingsSnapshotCapturesSettingsAndOwnsSlices(t *testing.T) {
	files := []string{"powershell-profile", "windows-powershell-profile"}
	profiles := []core.GitProfile{{ID: "work", Key: "example.com/work", Name: "Work"}}
	probes := fakeSettingsProbes()
	probes.hookFiles = func(shell core.Shell) []string {
		if shell != core.ShellPowerShell {
			t.Fatalf("hook files requested for %q", shell)
		}
		return files
	}
	reads := make(map[string]int)
	probes.hookInstalledIn = func(file string) bool {
		reads[file]++
		return file == files[1]
	}
	probes.gitRulesInstalled = func() bool { return true }
	probes.profiles = func() []core.GitProfile { return profiles }

	snapshot := readSettingsSnapshot(probes)
	if !snapshot.Ready || snapshot.Shell != core.ShellPowerShell || !snapshot.GitRulesInstalled {
		t.Fatalf("settings facts missing: %+v", snapshot)
	}
	if !snapshot.HookInstalled || snapshot.HookInstalledIn[files[0]] || !snapshot.HookInstalledIn[files[1]] {
		t.Fatalf("partial hook installation was lost: %+v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.HookFiles, files) || !reflect.DeepEqual(snapshot.Profiles, profiles) {
		t.Fatalf("settings lists differ: %+v", snapshot)
	}
	for _, file := range files {
		if reads[file] != 1 {
			t.Fatalf("hook file %q read %d times, want 1", file, reads[file])
		}
	}

	files[0] = "changed-profile"
	profiles[0].Name = "Changed"
	if snapshot.HookFiles[0] != "powershell-profile" || snapshot.Profiles[0].Name != "Work" {
		t.Fatal("snapshot shares mutable lists with its probes")
	}
}

func TestSettingsSnapshotWithoutHooks(t *testing.T) {
	probes := fakeSettingsProbes()
	probes.hookFiles = func(core.Shell) []string { return []string{"profile"} }
	snapshot := readSettingsSnapshot(probes)
	if !snapshot.Ready || snapshot.HookInstalled || snapshot.HookInstalledIn["profile"] {
		t.Fatalf("missing hooks reported as installed: %+v", snapshot)
	}
	if installed, present := snapshot.HookInstalledIn["profile"]; !present || installed {
		t.Fatal("snapshot omitted a checked startup file")
	}
}
