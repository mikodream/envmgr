package app

import "envmgr/internal/core"

// settingsSnapshot holds the facts needed to render settings without filesystem
// reads or Git subprocesses on the rendering path.
type settingsSnapshot struct {
	Ready                   bool
	Shell                   core.Shell
	HookScript              string
	HookFiles               []string
	HookInstalled           bool
	HookInstalledIn         map[string]bool
	GitRulesInstalled       bool
	GitRemoteRulesSupported bool
	Profiles                []core.GitProfile
}

// settingsProbes keeps environment reads local to one snapshot load and lets
// tests supply facts without changing shell profiles or global Git settings.
type settingsProbes struct {
	currentShell            func() core.Shell
	hookFiles               func(core.Shell) []string
	hookInstalledIn         func(string) bool
	gitRulesInstalled       func() bool
	gitRemoteRulesSupported func() bool
	profiles                func() []core.GitProfile
}

func loadSettingsSnapshot(svc *core.Service) settingsSnapshot {
	snapshot := readSettingsSnapshot(settingsProbes{
		currentShell:            core.CurrentShell,
		hookFiles:               core.HookFiles,
		hookInstalledIn:         core.HookInstalledIn,
		gitRulesInstalled:       svc.GitRulesInstalled,
		gitRemoteRulesSupported: core.IncludeIfByRemoteSupported,
		profiles:                svc.GitProfiles,
	})
	snapshot.HookScript = core.HookScript(snapshot.Shell)
	return snapshot
}

func readSettingsSnapshot(probes settingsProbes) settingsSnapshot {
	shell := probes.currentShell()
	files := append([]string(nil), probes.hookFiles(shell)...)
	snapshot := settingsSnapshot{
		Shell:                   shell,
		HookFiles:               files,
		HookInstalledIn:         make(map[string]bool, len(files)),
		GitRulesInstalled:       probes.gitRulesInstalled(),
		GitRemoteRulesSupported: probes.gitRemoteRulesSupported(),
		Profiles:                append([]core.GitProfile(nil), probes.profiles()...),
	}
	for _, file := range files {
		installed := probes.hookInstalledIn(file)
		snapshot.HookInstalledIn[file] = installed
		snapshot.HookInstalled = snapshot.HookInstalled || installed
	}
	snapshot.Ready = true
	return snapshot
}
