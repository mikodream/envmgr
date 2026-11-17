package core

import (
	"testing"
)

// fakeGit serves canned command output, so the resolution and parsing logic can
// be checked without a repository on disk.
type fakeGit struct {
	responses map[string]string
	fail      map[string]bool
	calls     []string
}

func (f *fakeGit) run(dir string, args ...string) (string, error) {
	key := joinArgs(args)
	f.calls = append(f.calls, key)
	if f.fail[key] {
		return "", errNotSet
	}
	if v, ok := f.responses[key]; ok {
		return v, nil
	}
	return "", errNotSet
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

type gitTestError string

func (e gitTestError) Error() string { return string(e) }

const errNotSet = gitTestError("exit status 1")

func okLook(string) (string, error) { return "/usr/bin/git", nil }
func noLook(string) (string, error) { return "", errNotSet }

// TestClassifyOrigin names the three kinds of setting that can answer, from the
// file git reported. Getting this wrong would attribute a remote-matched rule to
// the global config, and the user would go edit the wrong file.
func TestClassifyOrigin(t *testing.T) {
	rules := "/home/dev/.envmgr/git"
	env := gitEnv{rulesDir: rules}
	dir := "/home/dev/code/app"

	cases := []struct {
		name   string
		origin string
		want   Source
	}{
		{"repo config", ".git/config", SourceProject},
		{"repo config absolute", "/home/dev/code/app/.git/config", SourceProject},
		{"linked worktree config", "/home/dev/code/app/.git/worktrees/wt/config", SourceProject},
		{"envmgr rule file", rules + "/github.com__acme.gitconfig", SourceRule},
		{"plain global", "/home/dev/.gitconfig", SourceGlobal},
		{"global via xdg", "/home/dev/.config/git/config", SourceGlobal},
		{"nothing", "", SourceNone},
	}
	for _, c := range cases {
		if got := classifyOrigin(c.origin, dir, env); got != c.want {
			t.Errorf("%s: classifyOrigin(%q) = %q, want %q", c.name, c.origin, got, c.want)
		}
	}
	// Without a rules directory configured, a rule file is just a global file.
	if got := classifyOrigin(rules+"/x.gitconfig", dir, gitEnv{}); got != SourceGlobal {
		t.Errorf("with no rules dir, got %q, want global", got)
	}
}

// TestUnderDir must not treat a sibling directory with a shared prefix as
// inside, or a repository named "git-old" would be read as EnvMgr's own rules.
func TestUnderDir(t *testing.T) {
	dir := "/home/dev/.envmgr/git"
	inside := []string{
		dir + "/a.gitconfig",
		`\home\dev\.envmgr\git\a.gitconfig`,
		"/home/dev/.envmgr/git/sub/a.gitconfig",
	}
	for _, p := range inside {
		if !underDir(p, dir) {
			t.Errorf("underDir(%q, %q) = false, want true", p, dir)
		}
	}
	outside := []string{
		"/home/dev/.envmgr/git-old/a.gitconfig",
		"/home/dev/.gitconfig",
		dir,
	}
	for _, p := range outside {
		if underDir(p, dir) {
			t.Errorf("underDir(%q, %q) = true, want false", p, dir)
		}
	}
}

// TestSourceForNeverClaimsAGhost: an empty value has no source regardless of what
// file git would have read it from.
func TestSourceForNeverClaimsAGhost(t *testing.T) {
	if got := sourceFor("", "/home/dev/.gitconfig", "/repo", gitEnv{}); got != SourceNone {
		t.Errorf("empty value got source %q", got)
	}
	if got := sourceFor("Dev", ".git/config", "/repo", gitEnv{}); got != SourceProject {
		t.Errorf("value from repo config got %q", got)
	}
}

// TestLoadGitOutsideRepoStillReadsIdentity: a plain directory has no repository
// settings, but the global identity is what a commit would use after `git init`,
// so the page has to show it.
func TestLoadGitOutsideRepoStillReadsIdentity(t *testing.T) {
	f := &fakeGit{
		responses: map[string]string{
			// What `git config --show-origin --get` prints: the file that
			// answered, a tab, then the value.
			"config --show-origin --get user.name":  "file:/home/dev/.gitconfig\tGlobal Dev",
			"config --show-origin --get user.email": "file:/home/dev/.gitconfig\tdev@global.test",
		},
		fail: map[string]bool{"rev-parse --is-inside-work-tree": true},
	}
	info := loadGit("/some/dir", f.run, okLook, gitEnv{})
	if !info.Available {
		t.Fatal("git should be reported as available")
	}
	if info.Repo {
		t.Error("a plain directory is not a repository")
	}
	if info.Identity.Name != "Global Dev" || info.Identity.NameSource != SourceGlobal {
		t.Errorf("identity = %+v, want the global one", info.Identity)
	}
	if info.Identity.NameOrigin != "/home/dev/.gitconfig" {
		t.Errorf("origin = %q, want the global config path", info.Identity.NameOrigin)
	}
	if info.Identity.Complete() != true {
		t.Error("a name and email from global is a complete identity")
	}
}

// TestLoadGitMissingProgram reports git itself as absent, which is a different
// message from "this directory is not a repository".
func TestLoadGitMissingProgram(t *testing.T) {
	f := &fakeGit{responses: map[string]string{}}
	info := loadGit("/some/dir", f.run, noLook, gitEnv{})
	if info.Available {
		t.Error("git is not on PATH, so Available must be false")
	}
	if info.Repo {
		t.Error("no repository can be read without git")
	}
	if len(f.calls) != 0 {
		t.Errorf("no command should run without git, ran %v", f.calls)
	}
}

// TestLoadGitRepoState reads the branch, remote, and the staged/modified split.
func TestLoadGitRepoState(t *testing.T) {
	f := &fakeGit{responses: map[string]string{
		"rev-parse --is-inside-work-tree": "true",
		"rev-parse --show-toplevel":       "/repo",
		"symbolic-ref --short HEAD":       "main",
		"rev-parse --verify --quiet HEAD": "abc1234",
		"config --get remote.origin.url":  "git@github.com:acme/app.git",
		// One staged file, one modified-but-unstaged file, one untracked, and a
		// file that is both staged and modified again afterwards.
		"status --porcelain":                    "M  staged.txt\n M dirty.txt\n?? new.txt\nMM both.txt\n",
		"config --show-origin --get user.name":  "file:.git/config\tProject Dev",
		"config --show-origin --get user.email": "file:.git/config\tdev@project.test",
		"config --local --get user.name":        "Project Dev",
		"config --local --get user.email":       "dev@project.test",
	}}
	info := loadGit("/repo", f.run, okLook, gitEnv{})
	if !info.Repo || info.Root != "/repo" {
		t.Fatalf("repo state = %+v", info)
	}
	if info.Branch != "main" || info.Detached {
		t.Errorf("branch = %q, detached = %v", info.Branch, info.Detached)
	}
	if info.Remote != "git@github.com:acme/app.git" {
		t.Errorf("remote = %q", info.Remote)
	}
	if info.Staged != 2 { // staged.txt and both.txt
		t.Errorf("staged = %d, want 2", info.Staged)
	}
	if info.Modified != 2 { // dirty.txt and both.txt
		t.Errorf("modified = %d, want 2", info.Modified)
	}
	if info.Untracked != 1 {
		t.Errorf("untracked = %d, want 1", info.Untracked)
	}
	if info.Clean() {
		t.Error("four changed paths is not clean")
	}
	if info.Changed() != 5 {
		t.Errorf("changed = %d, want 5", info.Changed())
	}
	if info.Identity.NameSource != SourceProject {
		t.Errorf("name should come from the project, got %q", info.Identity.NameSource)
	}
}

// TestCountPorcelain covers the shapes git status can produce.
func TestCountPorcelain(t *testing.T) {
	cases := []struct {
		out                         string
		staged, modified, untracked int
	}{
		{"", 0, 0, 0},
		{"?? a.txt\n", 0, 0, 1},
		{"A  a.txt\n", 1, 0, 0},
		{" M a.txt\n", 0, 1, 0},
		{"D  a.txt\n", 1, 0, 0},
		{"R  old -> new\n", 1, 0, 0},
		{"?? a\n?? b\n M c\n", 0, 1, 2},
	}
	for _, c := range cases {
		s, m, u := countPorcelain(c.out)
		if s != c.staged || m != c.modified || u != c.untracked {
			t.Errorf("countPorcelain(%q) = %d/%d/%d, want %d/%d/%d",
				c.out, s, m, u, c.staged, c.modified, c.untracked)
		}
	}
}

// TestFreshRepoIsNotDetached is the case real git exposed: right after
// `git init` there is no commit, so `rev-parse --abbrev-ref HEAD` answers
// "HEAD". Reporting that as a detached checkout would be wrong, and the branch
// name still has to come from somewhere.
func TestFreshRepoIsNotDetached(t *testing.T) {
	f := &fakeGit{
		responses: map[string]string{
			"rev-parse --is-inside-work-tree": "true",
			"symbolic-ref --short HEAD":       "main",
		},
		fail: map[string]bool{
			"rev-parse --verify --quiet HEAD": true, // no commit yet
			"rev-parse --abbrev-ref HEAD":     true,
		},
	}
	info := loadGit("/repo", f.run, okLook, gitEnv{})
	if !info.Repo {
		t.Fatal("should be a repository")
	}
	if info.Detached {
		t.Error("an empty repository is on its default branch, not detached")
	}
	if info.Branch != "main" {
		t.Errorf("branch = %q, want main", info.Branch)
	}
	if info.Commits {
		t.Error("a repository without HEAD has no commits")
	}
}

// TestDetachedHead: with no symbolic HEAD and no branch name, the checkout is
// genuinely detached.
func TestDetachedHead(t *testing.T) {
	f := &fakeGit{
		responses: map[string]string{
			"rev-parse --is-inside-work-tree": "true",
			"rev-parse --abbrev-ref HEAD":     "HEAD",
		},
		fail: map[string]bool{
			"symbolic-ref --short HEAD":       true,
			"rev-parse --verify --quiet HEAD": true,
		},
	}
	info := loadGit("/repo", f.run, okLook, gitEnv{})
	if !info.Detached {
		t.Error("no symbolic HEAD means a detached checkout")
	}
	if info.Branch != "" {
		t.Errorf("a detached checkout has no branch name, got %q", info.Branch)
	}
}

// TestGitIdentityComplete requires both fields, since git refuses to commit
// with only one of them configured.
func TestGitIdentityComplete(t *testing.T) {
	if (GitIdentity{Name: "a"}).Complete() {
		t.Error("a name without an email is not complete")
	}
	if (GitIdentity{Email: "a@b"}).Complete() {
		t.Error("an email without a name is not complete")
	}
	if !(GitIdentity{Name: "a", Email: "a@b"}).Complete() {
		t.Error("both fields is complete")
	}
}
