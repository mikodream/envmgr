package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// These tests drive the real git program. They are skipped when git is not
// installed, so the suite still runs on a bare machine.

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// tempRepo makes a directory with an isolated global config, so a test never
// reads or writes the real ~/.gitconfig.
func tempRepo(t *testing.T) (dir string, setGlobal func(name, email string)) {
	t.Helper()
	gitOrSkip(t)
	home := t.TempDir()
	globalCfg := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(globalCfg, []byte("[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalCfg)
	// Keep the system config out of it too, or a machine-wide setting would
	// make the "unset" cases pass for the wrong reason.
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "gitconfig-system"))
	if err := os.WriteFile(filepath.Join(home, "gitconfig-system"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	setGlobal = func(name, email string) {
		if name != "" {
			runGit(dir, "config", "--global", "user.name", name)
		}
		if email != "" {
			runGit(dir, "config", "--global", "user.email", email)
		}
	}
	return dir, setGlobal
}

// TestGitInitAndIdentityRoundTrip is the whole feature against real git:
// initialise, write a project identity, read it back, then clear it and watch
// the global value return.
func TestGitInitAndIdentityRoundTrip(t *testing.T) {
	dir, setGlobal := tempRepo(t)
	setGlobal("Global Dev", "global@test.dev")

	// Before init: no repository, but the global identity is known.
	before := LoadGit(dir)
	if before.Repo {
		t.Fatal("a fresh directory is not a repository")
	}
	if before.Identity.Name != "Global Dev" || before.Identity.NameSource != SourceGlobal {
		t.Fatalf("identity before init = %+v, want the global one", before.Identity)
	}

	info, err := InitGitRepo(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !info.Repo {
		t.Fatal("after init the directory is a repository")
	}
	if info.Detached {
		t.Error("a new repository is not detached")
	}
	if info.Branch == "" {
		t.Error("a new repository still has a branch name")
	}
	if info.Commits {
		t.Error("a new repository has no commit yet")
	}

	// Write a project identity.
	if err := WriteGitIdentity(dir, "Project Dev", "proj@test.dev"); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	after := LoadGit(dir)
	if after.Identity.Name != "Project Dev" || after.Identity.NameSource != SourceProject {
		t.Errorf("name = %q from %q, want the project one", after.Identity.Name, after.Identity.NameSource)
	}
	if after.Identity.Email != "proj@test.dev" {
		t.Errorf("email = %q", after.Identity.Email)
	}
	// The global value is still remembered, for the editor's placeholder.
	if after.Identity.GlobalName != "Global Dev" {
		t.Errorf("global name = %q", after.Identity.GlobalName)
	}

	// git itself must agree, not just our reading of it.
	if got, _ := runGit(dir, "config", "--local", "--get", "user.name"); got != "Project Dev" {
		t.Errorf("git config --local user.name = %q", got)
	}

	// Clearing the fields removes the override and the global value returns.
	if err := WriteGitIdentity(dir, "", ""); err != nil {
		t.Fatalf("clear identity: %v", err)
	}
	cleared := LoadGit(dir)
	if cleared.Identity.Name != "Global Dev" || cleared.Identity.NameSource != SourceGlobal {
		t.Errorf("after clearing, name = %q from %q, want the global one",
			cleared.Identity.Name, cleared.Identity.NameSource)
	}
	if got, err := runGit(dir, "config", "--local", "--get", "user.name"); err == nil {
		t.Errorf("the local setting should be gone, git still says %q", got)
	}
}

// TestGitRootedInParentRepository: a project inside a bigger repository is not
// its own repository, and the panel has to be able to say so.
func TestGitRootedInParentRepository(t *testing.T) {
	dir, _ := tempRepo(t)
	if _, err := InitGitRepo(dir); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "apps", "web")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	info := LoadGit(nested)
	if !info.Repo {
		t.Fatal("a nested directory is inside a work tree")
	}
	if sameDirectory(info.Root, nested) {
		t.Errorf("root = %q, should be the parent repository %q", info.Root, dir)
	}
}

// TestGitStatusCountsRealChanges checks the porcelain parsing against files
// git actually reports.
func TestGitStatusCountsRealChanges(t *testing.T) {
	dir, setGlobal := tempRepo(t)
	setGlobal("Dev", "dev@test.dev")
	if _, err := InitGitRepo(dir); err != nil {
		t.Fatal(err)
	}
	if err := WriteGitIdentity(dir, "Dev", "dev@test.dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(dir, "commit", "-m", "init"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	clean := LoadGit(dir)
	if !clean.Commits {
		t.Error("after a commit the repository has commits")
	}
	if !clean.Clean() {
		t.Errorf("expected a clean tree, got staged=%d modified=%d untracked=%d",
			clean.Staged, clean.Modified, clean.Untracked)
	}

	// One modified file and one untracked file.
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := LoadGit(dir)
	if dirty.Modified != 1 || dirty.Untracked != 1 {
		t.Errorf("modified=%d untracked=%d, want 1 and 1", dirty.Modified, dirty.Untracked)
	}
	if dirty.Clean() {
		t.Error("two changes is not clean")
	}
}

// sameDirectory compares two paths as the platform would.
func sameDirectory(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(p)
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			p = filepath.ToSlash(p)
			return lowerASCII(p)
		}
		return p
	}
	return clean(a) == clean(b)
}

func lowerASCII(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + ('a' - 'A')
		}
	}
	return string(out)
}
