package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"envmgr/internal/core"
)

// No tests run in parallel: HOME, Git's config paths, and stdout are process-wide.
func isolatedGitCLI(t *testing.T) (*core.Service, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Global\n\temail = global@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	dir := filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := core.InitGitRepo(dir); err != nil {
		t.Fatal(err)
	}
	svc, err := core.NewService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddProject(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b, err := os.ReadFile(global)
		if err != nil || string(b) != "[user]\n\tname = Global\n\temail = global@example.com\n" {
			t.Errorf("CLI changed global Git configuration: %q, %v", b, err)
		}
	})
	return svc, dir
}

func TestGitRemoteCLI(t *testing.T) {
	svc, dir := isolatedGitCLI(t)
	urls := map[string]string{
		"github": "git@github.com:acme/app.git",
		"gitee":  "https://gitee.com/acme/app.git",
		"gitea":  "ssh://git@gitea.example.com/acme/app.git",
	}
	for _, name := range []string{"github", "gitee", "gitea"} {
		if code := cmdGit(svc, []string{"remote", "add", name, urls[name], "--dir", "project"}); code != 0 {
			t.Fatalf("add %s: exit %d", name, code)
		}
	}
	// A second add updates exactly this remote, retaining the other destinations.
	urls["gitee"] = "git@gitee.com:acme/new.git"
	if code := cmdGit(svc, []string{"--dir=" + dir, "remote", "add", "gitee", urls["gitee"]}); code != 0 {
		t.Fatalf("update remote: exit %d", code)
	}
	pushURL := "ssh://git@push.example.com/acme/app.git"
	if out, err := exec.Command("git", "-C", dir, "remote", "set-url", "--push", "github", pushURL).CombinedOutput(); err != nil {
		t.Fatalf("set distinct push URL: %v: %s", err, out)
	}
	// Read the external push URL change without waiting for the service cache.
	var err error
	svc, err = core.NewService()
	if err != nil {
		t.Fatal(err)
	}
	info := core.LoadGit(dir)
	if len(info.Remotes) != 3 {
		t.Fatalf("remotes = %+v", info.Remotes)
	}
	for _, remote := range info.Remotes {
		if remote.URL != urls[remote.Name] {
			t.Errorf("remote = %+v", remote)
		}
	}
	for _, args := range [][]string{{"remote", "list", "--dir", dir}, {"status", "--dir", dir}} {
		output := captureCLIOutput(t, func() {
			if code := cmdGit(svc, args); code != 0 {
				t.Errorf("%v: exit %d", args, code)
			}
		})
		if !strings.Contains(output, pushURL) {
			t.Errorf("%v missing distinct push URL: %s", args, output)
		}
		for name, url := range urls {
			if !strings.Contains(output, name) || !strings.Contains(output, url) {
				t.Errorf("%v missing %s / %s: %s", args, name, url, output)
			}
		}
	}
	if code := cmdGit(svc, []string{"remote", "rm", "gitee", "--dir", "project"}); code != 0 {
		t.Fatalf("remove remote: exit %d", code)
	}
	if got := core.LoadGit(dir).Remotes; len(got) != 2 {
		t.Fatalf("remotes after removal = %+v", got)
	}
}

func TestGitProfileMultipleIdentitiesCLI(t *testing.T) {
	svc, dir := isolatedGitCLI(t)
	for _, name := range []string{"Work", "Personal"} {
		if code := cmdGit(svc, []string{"profile", "add", "git@github.com:acme/app.git", "--name", name, "--email", strings.ToLower(name) + "@example.com", "--label", name}); code != 0 {
			t.Fatalf("add identity %s: exit %d", name, code)
		}
	}
	profiles := svc.GitProfiles()
	if len(profiles) != 2 || profiles[0].IdentityID() == profiles[1].IdentityID() {
		t.Fatalf("distinct identities = %+v", profiles)
	}
	selected := profiles[1]
	// Switch in both directions, including returning to a previously used ID.
	for _, p := range []core.GitProfile{selected, profiles[0], selected} {
		if code := cmdGit(svc, []string{"profile", "use", p.IdentityID(), "--dir", "project"}); code != 0 {
			t.Fatalf("switch identity %s: exit %d", p.IdentityID(), code)
		}
	}
	if id := core.LoadGit(dir).Identity; id.Name != selected.Name || id.Email != selected.Email || id.NameSource != core.SourceProject {
		t.Fatalf("selected identity = %+v, want %+v", id, selected)
	}
	for _, p := range svc.GitProfiles() {
		if p.Active != (p.IdentityID() == selected.IdentityID()) {
			t.Errorf("unexpected active state: %+v", p)
		}
	}
	output := captureCLIOutput(t, func() { cmdGit(svc, []string{"profile", "list"}) })
	for _, p := range profiles {
		if !strings.Contains(output, p.IdentityID()) {
			t.Errorf("list missing ID %s: %s", p.IdentityID(), output)
		}
	}
	if !strings.Contains(output, "已启用") || !strings.Contains(output, "未启用") {
		t.Errorf("list missing active state: %s", output)
	}
	if code := cmdGit(svc, []string{"profile", "edit", selected.IdentityID(), "--name", "Updated"}); code != 0 {
		t.Fatalf("edit identity: exit %d", code)
	}
	if got := svc.GitProfiles(); len(got) != 2 {
		t.Fatalf("edit duplicated identity: %+v", got)
	} else {
		for _, p := range got {
			if p.IdentityID() == selected.IdentityID() && (p.Name != "Updated" || p.Email != selected.Email) {
				t.Errorf("edited profile = %+v", p)
			}
		}
	}
	if code := cmdGit(svc, []string{"profile", "rm", selected.IdentityID()}); code != 0 {
		t.Fatalf("remove by ID: exit %d", code)
	}
	if got := svc.GitProfiles(); len(got) != 1 || got[0].IdentityID() == selected.IdentityID() {
		t.Fatalf("remove deleted wrong identity: %+v", got)
	}
	if code := cmdGit(svc, []string{"profile", "rm", "https://github.com/acme/app.git"}); code != 0 || len(svc.GitProfiles()) != 0 {
		t.Fatalf("legacy address removal: exit %d, profiles %+v", code, svc.GitProfiles())
	}
}

func TestGitCLIRejectsMalformedArguments(t *testing.T) {
	svc, dir := isolatedGitCLI(t)
	for _, args := range [][]string{
		{"remote", "add"}, {"remote", "add", "origin"},
		{"remote", "add", "origin", "https://example.com/a.git", "extra"},
		{"remote", "rm"}, {"remote", "list", "extra"},
		{"remote", "unknown"}, {"profile", "use"},
		{"profile", "use", "one", "two"}, {"profile", "edit", "one"},
		{"profile", "add", "github.com/acme", "extra", "--name", "A", "--email", "a@example.com"},
	} {
		if code := cmdGit(svc, append(args, "--dir", dir)); code != 2 {
			t.Errorf("%v: exit %d, want usage error", args, code)
		}
	}
	for _, args := range [][]string{
		{"remote", "list", "--dir"},
		{"profile", "use", "one", "--dir", "--name", "A"},
		{"profile", "add", "github.com/acme", "--name"},
	} {
		if code := cmdGit(svc, args); code != 2 {
			t.Errorf("missing flag value %v: exit %d", args, code)
		}
	}
	if len(svc.GitProfiles()) != 0 || len(core.LoadGit(dir).Remotes) != 0 {
		t.Fatal("malformed arguments changed repository or profiles")
	}
}
