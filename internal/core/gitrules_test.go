package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the real git program with real config files, because the
// whole feature depends on git's own conditional-include matching. A unit test of
// the generated text would not prove that git agrees with it.

// isolateGit points git at throwaway config files, so a test never reads or
// writes the real global git config.
func isolateGit(t *testing.T) (globalPath string, home string) {
	t.Helper()
	if !GitAvailable() {
		t.Skip("git is not installed")
	}
	home = t.TempDir()
	globalPath = filepath.Join(home, "gitconfig")
	body := "[user]\n\tname = Global Person\n\temail = global@person.dev\n" +
		"[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(globalPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalPath)
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "gitconfig-system"))
	if err := os.WriteFile(filepath.Join(home, "gitconfig-system"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// EnvMgr's own directory for profile files follows HOME, so point both at
	// the temporary home; otherwise the test would write into the real ~/.envmgr.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return globalPath, home
}

// makeRepo creates a repository with one remote.
func makeRepo(t *testing.T, parent, name, remote string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "init"); err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
	if remote != "" {
		if _, err := runGit(dir, "remote", "add", "origin", remote); err != nil {
			t.Fatalf("remote add: %v", err)
		}
	}
	return dir
}

// TestRulesSelectIdentityByRemote is the request, end to end: one binding per
// remote address, and git picks the right person for each repository.
func TestRulesSelectIdentityByRemote(t *testing.T) {
	globalPath, home := isolateGit(t)
	work := filepath.Join(home, "code")

	profiles := []GitProfile{
		{Key: "github.com/acme", Name: "Acme Bot", Email: "bot@acme.dev"},
		{Key: "gitlab.com/personal", Name: "Solo Dev", Email: "me@personal.dev"},
		// A single repository, to prove the repository-level ending works: a
		// clone URL normally ends in .git, while the key has it stripped.
		{Key: "github.com/one/repo", Name: "One Repo", Email: "one@repo.dev"},
	}
	if err := InstallGitRules(profiles); err != nil {
		t.Fatalf("InstallGitRules: %v", err)
	}

	// The plain global identity must survive: rules add, they never replace.
	if v, _ := runGit(home, "config", "--file", globalPath, "--get", "user.name"); v != "Global Person" {
		t.Errorf("plain global user.name = %q, want Global Person", v)
	}

	// Clones of the same addresses, in different spellings.
	repos := []struct {
		remote string
		want   string
	}{
		{"git@github.com:acme/app.git", "Acme Bot"},
		{"git@github.com:acme/another.git", "Acme Bot"},
		{"https://github.com/acme/third.git", "Acme Bot"},
		{"ssh://git@github.com:2222/acme/fourth.git", "Acme Bot"},
		{"https://token@github.com/acme/fifth", "Acme Bot"},
		{"git@gitlab.com:personal/notes.git", "Solo Dev"},
		{"https://gitlab.com/personal/blog.git", "Solo Dev"},
		// A repository-level binding: the URL ends in .git, the key does not.
		{"git@github.com:one/repo.git", "One Repo"},
		{"https://github.com/one/repo.git", "One Repo"},
		{"https://github.com/one/repo", "One Repo"},
		// A sibling in the same organisation must keep its own identity.
		{"git@github.com:one/other.git", "Global Person"},
		// Not bound: falls through to the global identity.
		{"git@github.com:someoneelse/thing.git", "Global Person"},
		{"git@bitbucket.org:acme/app.git", "Global Person"},
	}
	for i, c := range repos {
		dir := makeRepo(t, work, "r"+string(rune('a'+i)), c.remote)
		if got := effectiveName(t, dir); got != c.want {
			t.Errorf("remote %q → identity %q, want %q", c.remote, got, c.want)
		}
	}
}

// TestRulesProduceRuleSource: with the rules installed, the reader must attribute
// the value to the rule, not to the plain global config.
func TestRulesProduceRuleSource(t *testing.T) {
	_, home := isolateGit(t)
	work := filepath.Join(home, "code")
	profiles := []GitProfile{{Key: "github.com/acme", Name: "Acme Bot", Email: "bot@acme.dev"}}
	if err := InstallGitRules(profiles); err != nil {
		t.Fatalf("InstallGitRules: %v", err)
	}
	dir := makeRepo(t, work, "app", "git@github.com:acme/app.git")

	info := LoadGitIn(dir, gitEnv{rulesDir: GitConfigDir(), globalPath: GlobalGitConfig()})
	if info.Identity.NameSource != SourceRule {
		t.Errorf("name source = %q, want %q (origin %q)",
			info.Identity.NameSource, SourceRule, info.Identity.NameOrigin)
	}
	if info.Identity.Name != "Acme Bot" || info.Identity.Email != "bot@acme.dev" {
		t.Errorf("identity = %q <%s>", info.Identity.Name, info.Identity.Email)
	}
	// The editor needs the plain global value for its "fall back to global" hint,
	// and that value must not be the rule's.
	if info.Identity.GlobalName != "Global Person" {
		t.Errorf("plain global name = %q, want Global Person", info.Identity.GlobalName)
	}

	// A project-level setting still wins over the rule, as git's own precedence
	// requires.
	if err := WriteGitIdentity(dir, "Repo Person", "repo@acme.dev"); err != nil {
		t.Fatal(err)
	}
	after := LoadGitIn(dir, gitEnv{rulesDir: GitConfigDir(), globalPath: GlobalGitConfig()})
	if after.Identity.NameSource != SourceProject || after.Identity.Name != "Repo Person" {
		t.Errorf("after a local write: %q from %q, want Repo Person from project",
			after.Identity.Name, after.Identity.NameSource)
	}
}

// TestInstallGitRulesIsIdempotent: saving twice must not stack blocks, or the
// global config would grow without bound.
func TestInstallGitRulesIsIdempotent(t *testing.T) {
	globalPath, _ := isolateGit(t)
	profiles := []GitProfile{
		{Key: "github.com/acme", Name: "Acme Bot", Email: "bot@acme.dev"},
		{Key: "gitlab.com/team", Name: "Team", Email: "team@corp.dev"},
	}
	for i := 0; i < 3; i++ {
		if err := InstallGitRules(profiles); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if n := strings.Count(text, gitRulesBegin); n != 1 {
		t.Errorf("the block appears %d times, want 1:\n%s", n, text)
	}
	if n := strings.Count(text, "[user]"); n != 1 {
		t.Errorf("the original [user] section was disturbed (%d occurrences):\n%s", n, text)
	}
	// A backup of the pre-EnvMgr file is kept.
	backup, err := os.ReadFile(globalPath + ".envmgr.bak")
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if strings.Contains(string(backup), gitRulesBegin) {
		t.Error("the backup should be the file as it was before EnvMgr")
	}
}

// TestUninstallGitRulesRestoresTheFile: removing the rules must leave the global
// config exactly as it was.
func TestUninstallGitRulesRestoresTheFile(t *testing.T) {
	globalPath, _ := isolateGit(t)
	before, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallGitRules([]GitProfile{{Key: "github.com/acme", Name: "A", Email: "a@x.dev"}}); err != nil {
		t.Fatal(err)
	}
	removed, err := UninstallGitRules()
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("uninstall reported nothing to remove")
	}
	after, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(after)) != strings.TrimSpace(string(before)) {
		t.Errorf("global config differs after uninstall:\n%q\nwant\n%q", after, before)
	}
	if GitRulesInstalled() {
		t.Error("GitRulesInstalled still reports true")
	}
	// Uninstalling twice is harmless.
	if removed, err := UninstallGitRules(); err != nil || removed {
		t.Errorf("second uninstall = %v, %v", removed, err)
	}
}

// TestUninstallKeepsUserIncludes: a hand-written includeIf in the global config
// must survive both install and uninstall.
func TestUninstallKeepsUserIncludes(t *testing.T) {
	globalPath, home := isolateGit(t)
	userOwn := filepath.ToSlash(filepath.Join(home, "mine.gitconfig"))
	if err := os.WriteFile(filepath.Join(home, "mine.gitconfig"),
		[]byte("[user]\n\tname = Mine\n\temail = mine@x.dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "[user]\n\tname = Global Person\n\temail = global@person.dev\n" +
		"[includeIf \"gitdir:~/werk/\"]\n\tpath = " + userOwn + "\n"
	if err := os.WriteFile(globalPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InstallGitRules([]GitProfile{{Key: "github.com/acme", Name: "A", Email: "a@x.dev"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(data), "gitdir:~/werk/") {
		t.Fatal("install removed the user's own includeIf")
	}
	if _, err := UninstallGitRules(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(globalPath)
	if !strings.Contains(string(data), "gitdir:~/werk/") {
		t.Error("uninstall removed the user's own includeIf")
	}
	if !strings.Contains(string(data), "Global Person") {
		t.Error("uninstall removed the plain global identity")
	}
}

// effectiveName asks git itself, which is the only opinion that counts.
func effectiveName(t *testing.T, dir string) string {
	t.Helper()
	out, err := runGit(dir, "config", "--get", "user.name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// TestGitVersionParsing: the conditional include by remote arrived in 2.36, and
// the UI has to know whether this git can do it.
func TestGitVersionParsing(t *testing.T) {
	major, minor, ok := GitVersion()
	if !ok {
		t.Skip("git is not installed")
	}
	if major < 2 {
		t.Errorf("parsed an implausible version %d.%d", major, minor)
	}
	// The feature is reported as supported on anything this new, and the check
	// must be a real comparison rather than a constant.
	if major == 2 && minor < 36 && IncludeIfByRemoteSupported() {
		t.Errorf("git %d.%d cannot support includeIf by remote", major, minor)
	}
	if major > 2 && !IncludeIfByRemoteSupported() {
		t.Errorf("git %d.%d should support includeIf by remote", major, minor)
	}
}
