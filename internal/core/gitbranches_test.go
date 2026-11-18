package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func branchTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitOperation(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func branchTestRepo(t *testing.T) string {
	t.Helper()
	gitOrSkip(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		path := filepath.Join(home, key)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS"} {
		t.Setenv(key, "") // Register restoration, then remove path variables entirely.
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, "repo")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	branchTestGit(t, dir, "init", "-b", "main")
	branchTestGit(t, dir, "config", "--local", "user.name", "Branch Tester")
	branchTestGit(t, dir, "config", "--local", "user.email", "branches@example.test")
	branchTestWrite(t, dir, "base\n")
	branchTestGit(t, dir, "add", "tracked.txt")
	branchTestGit(t, dir, "commit", "-m", "base")
	return dir
}

func branchTestWrite(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func branchTestFind(t *testing.T, info GitInfo, ref string) GitBranch {
	t.Helper()
	for _, b := range info.Branches {
		if b.Ref == ref {
			return b
		}
	}
	t.Fatalf("missing %s in %+v", ref, info.Branches)
	return GitBranch{}
}

func TestGitBranchesCachedEnumerationAndTracking(t *testing.T) {
	dir := branchTestRepo(t)
	// No command contacts a server: remote refs are populated directly.
	for _, name := range []string{"team/origin", "origin"} {
		branchTestGit(t, dir, "remote", "add", name, filepath.Join(t.TempDir(), "offline.git"))
	}
	// Git remote add guards overlapping names; existing config can still contain
	// them. Keep the shorter remote without a fetch mapping to avoid ambiguity.
	branchTestGit(t, dir, "config", "--local", "remote.team.url", filepath.Join(t.TempDir(), "offline.git"))
	base := branchTestGit(t, dir, "rev-parse", "HEAD")
	branchTestGit(t, dir, "update-ref", "refs/remotes/team/origin/feature/path", base)
	branchTestGit(t, dir, "update-ref", "refs/remotes/origin/main", base)
	branchTestGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	branchTestGit(t, dir, "branch", "--set-upstream-to=origin/main", "main")
	branchTestWrite(t, dir, "local\n")
	branchTestGit(t, dir, "commit", "-am", "local")
	branchTestGit(t, dir, "switch", "--detach", base)
	branchTestWrite(t, dir, "remote\n")
	branchTestGit(t, dir, "commit", "-am", "remote")
	branchTestGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	branchTestGit(t, dir, "switch", "main")
	info := LoadGit(dir)
	local := branchTestFind(t, info, "refs/heads/main")
	if !local.Current || local.Remote != "" || local.Upstream != "refs/remotes/origin/main" || local.Ahead != 1 || local.Behind != 1 {
		t.Fatalf("local = %+v", local)
	}
	remote := branchTestFind(t, info, "refs/remotes/team/origin/feature/path")
	if remote.Remote != "team/origin" || remote.Name != "feature/path" || remote.Current {
		t.Fatalf("remote = %+v", remote)
	}
	for _, b := range info.Branches {
		if b.Ref == "refs/remotes/origin/HEAD" {
			t.Fatal("included symbolic remote HEAD")
		}
	}
	if info.PushRemote != "origin" {
		t.Fatalf("push fallback = %q", info.PushRemote)
	}
	if err := SwitchGitBranch(dir, remote.Ref); err != nil {
		t.Fatal(err)
	}
	tracking := branchTestFind(t, LoadGit(dir), "refs/heads/feature/path")
	if !tracking.Current || tracking.Upstream != remote.Ref {
		t.Fatalf("tracking = %+v", tracking)
	}
	if err := SwitchGitBranch(dir, "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	if err := SwitchGitBranch(dir, remote.Ref); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected local conflict, got %v", err)
	}
	for _, ref := range []string{"main", "HEAD", "refs/tags/main", "refs/heads/missing", "refs/remotes/origin/HEAD", "refs/heads/main~1"} {
		if err := SwitchGitBranch(dir, ref); err == nil {
			t.Errorf("accepted %q", ref)
		}
	}
}

func TestGitBranchesCreateSwitchDeleteSafety(t *testing.T) {
	dir := branchTestRepo(t)
	for _, name := range []string{"", "bad name", "--detach", "-force", "bad..name", "refs/heads/../bad"} {
		if err := CreateGitBranch(dir, name); err == nil {
			t.Errorf("created invalid %q", name)
		}
	}
	if err := CreateGitBranch(dir, "feature/path"); err != nil {
		t.Fatal(err)
	}
	if got := LoadGit(dir).Branch; got != "feature/path" {
		t.Fatalf("created branch = %q", got)
	}
	if err := DeleteGitBranch(dir, "feature/path"); err == nil || !strings.Contains(err.Error(), "checked out") && !strings.Contains(err.Error(), "used by worktree") {
		t.Fatalf("expected current-branch Git error: %v", err)
	}
	if err := SwitchGitBranch(dir, "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteGitBranch(dir, "feature/path"); err != nil {
		t.Fatal(err)
	}
	if err := CreateGitBranch(dir, "unmerged"); err != nil {
		t.Fatal(err)
	}
	branchTestWrite(t, dir, "unmerged\n")
	branchTestGit(t, dir, "commit", "-am", "unmerged")
	if err := SwitchGitBranch(dir, "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteGitBranch(dir, "unmerged"); err == nil || !strings.Contains(err.Error(), "not fully merged") {
		t.Fatalf("expected unmerged Git error: %v", err)
	}
	branchTestWrite(t, dir, "dirty\n")
	if err := SwitchGitBranch(dir, "refs/heads/unmerged"); err == nil || !strings.Contains(err.Error(), "overwritten") {
		t.Fatalf("expected dirty Git error: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	if err != nil || string(content) != "dirty\n" {
		t.Fatalf("dirty data changed: %q, %v", content, err)
	}
	if got := LoadGit(dir).Branch; got != "main" {
		t.Fatalf("failed switch changed branch: %q", got)
	}
	if err := CreateGitBranch(dir, "dirty-safe"); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(filepath.Join(dir, "tracked.txt"))
	if string(content) != "dirty\n" {
		t.Fatal("create lost dirty contents")
	}
	for _, name := range []string{"--force", "bad name", "missing"} {
		if err := DeleteGitBranch(dir, name); err == nil {
			t.Errorf("deleted %q", name)
		}
	}
}

func TestGitBranchesDefaultRemoteLocalPersistence(t *testing.T) {
	dir := branchTestRepo(t)
	global := os.Getenv("GIT_CONFIG_GLOBAL")
	branchTestGit(t, dir, "config", "--global", "remote.pushDefault", "globalremote")
	before, _ := os.ReadFile(global)
	for _, name := range []string{"backup", "team/origin", "origin"} {
		branchTestGit(t, dir, "remote", "add", name, filepath.Join(t.TempDir(), "offline.git"))
	}
	branchTestGit(t, dir, "config", "--local", "branch.main.pushRemote", "origin")
	branchTestGit(t, dir, "config", "--local", "branch.other.pushRemote", "backup")
	branchTestGit(t, dir, "config", "--local", "branch.main.remote", "origin")
	branchTestGit(t, dir, "config", "--local", "branch.main.merge", "refs/heads/main")
	if got := LoadGit(dir).PushRemote; got != "origin" {
		t.Fatalf("branch override = %q", got)
	}
	if err := SetGitDefaultRemote(dir, "team/origin"); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"remote.pushDefault": "team/origin", "branch.main.pushRemote": "team/origin", "branch.other.pushRemote": "backup", "branch.main.remote": "origin", "branch.main.merge": "refs/heads/main"} {
		if got := branchTestGit(t, dir, "config", "--local", "--get", key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := LoadGit(dir).PushRemote; got != "team/origin" {
		t.Fatalf("selected remote = %q", got)
	}
	for _, name := range []string{"", "missing", "--help", "team"} {
		if err := SetGitDefaultRemote(dir, name); err == nil {
			t.Errorf("selected invalid %q", name)
		}
	}
	if got := branchTestGit(t, dir, "config", "--local", "--get", "remote.pushDefault"); got != "team/origin" {
		t.Fatalf("invalid selection mutated config: %q", got)
	}
	after, _ := os.ReadFile(global)
	if string(before) != string(after) {
		t.Fatal("global config changed")
	}
	// A repository without origin falls back to the first named remote.
	branchTestGit(t, dir, "config", "--local", "--unset", "branch.main.pushRemote")
	branchTestGit(t, dir, "config", "--local", "--unset", "remote.pushDefault")
	branchTestGit(t, dir, "config", "--global", "--unset", "remote.pushDefault")
	branchTestGit(t, dir, "remote", "remove", "origin")
	if got := LoadGit(dir).PushRemote; got != "backup" {
		t.Fatalf("first remote fallback = %q", got)
	}
}

func TestGitBranchesClearRemovedRemoteSelections(t *testing.T) {
	dir := branchTestRepo(t)
	global := os.Getenv("GIT_CONFIG_GLOBAL")
	removed := "team/origin"
	branchTestGit(t, dir, "config", "--global", "remote.pushDefault", removed)
	branchTestGit(t, dir, "config", "--global", "branch.global.pushRemote", removed)
	before, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"remote.pushDefault":             removed,
		"branch.main.pushRemote":         removed,
		"branch.feature/path.pushRemote": removed,
		"branch.other.pushRemote":        "backup",
		"branch.main.remote":             removed,
		"branch.main.merge":              "refs/heads/main",
	} {
		branchTestGit(t, dir, "config", "--local", key, value)
	}
	// A key can have multiple values; remove only matching values of that key.
	branchTestGit(t, dir, "config", "--local", "--add", "branch.multi.pushRemote", removed)
	branchTestGit(t, dir, "config", "--local", "--add", "branch.multi.pushRemote", "backup")
	branchTestGit(t, dir, "config", "--local", "--add", "branch.multi.pushRemote", removed)
	if err := clearGitRemoteSelection(dir, removed); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"remote.pushDefault", "branch.main.pushRemote", "branch.feature/path.pushRemote"} {
		if out, err := runGitOperation(dir, "config", "--local", "--get", key); err == nil {
			t.Errorf("retained %s=%q", key, out)
		}
	}
	for key, want := range map[string]string{"branch.other.pushRemote": "backup", "branch.multi.pushRemote": "backup", "branch.main.remote": removed, "branch.main.merge": "refs/heads/main"} {
		if got := branchTestGit(t, dir, "config", "--local", "--get-all", key); got != want {
			t.Errorf("%s=%q, want %q", key, got, want)
		}
	}
	if err := clearGitRemoteSelection(dir, removed); err != nil {
		t.Fatal(err)
	}
	if err := clearGitRemoteSelection(dir, "backup"); err != nil {
		t.Fatal(err)
	}
	if err := clearGitRemoteSelection(dir, "backup"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(global)
	if err != nil || string(before) != string(after) {
		t.Fatalf("cleanup changed global config: %v", err)
	}
}

func TestGitBranchesFetchSelectedRemote(t *testing.T) {
	dir := branchTestRepo(t)
	global := os.Getenv("GIT_CONFIG_GLOBAL")
	before, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	base := branchTestGit(t, dir, "rev-parse", "HEAD")
	selected := filepath.Join(t.TempDir(), "selected.git")
	other := filepath.Join(t.TempDir(), "other.git")
	for _, bare := range []string{selected, other} {
		branchTestGit(t, dir, "clone", "--bare", "--", dir, bare)
	}
	branchTestGit(t, selected, "update-ref", "refs/heads/feature/path", base)
	branchTestGit(t, other, "update-ref", "refs/heads/other-only", base)
	branchTestGit(t, dir, "remote", "add", "team/origin", selected)
	branchTestGit(t, dir, "remote", "add", "backup", other)
	if err := SetGitDefaultRemote(dir, "team/origin"); err != nil {
		t.Fatal(err)
	}
	if refs := branchTestGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/remotes/"); refs != "" {
		t.Fatalf("remote selection fetched refs: %q", refs)
	}
	branchTestWrite(t, dir, "dirty during fetch\n")
	if err := FetchGitRemote(context.Background(), dir, "team/origin"); err != nil {
		t.Fatal(err)
	}
	info := LoadGit(dir)
	b := branchTestFind(t, info, "refs/remotes/team/origin/feature/path")
	if b.Name != "feature/path" || b.Remote != "team/origin" {
		t.Fatalf("fetched branch = %+v", b)
	}
	for _, b := range info.Branches {
		if b.Remote == "backup" {
			t.Fatalf("fetched another remote: %+v", b)
		}
	}
	if got := branchTestGit(t, dir, "rev-parse", "HEAD"); got != base {
		t.Fatalf("fetch changed HEAD: %q", got)
	}
	content, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	if err != nil || string(content) != "dirty during fetch\n" {
		t.Fatalf("fetch changed worktree: %q, %v", content, err)
	}
	// Refreshing the selected remote also removes its deleted cached branches.
	branchTestGit(t, selected, "update-ref", "-d", "refs/heads/feature/path")
	if err := FetchGitRemote(context.Background(), dir, "team/origin"); err != nil {
		t.Fatal(err)
	}
	if refs := branchTestGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/remotes/team/origin/feature/path"); refs != "" {
		t.Fatalf("deleted remote branch was not pruned: %q", refs)
	}
	for _, name := range []string{"", "missing", "--all", selected} {
		if err := FetchGitRemote(context.Background(), dir, name); err == nil {
			t.Errorf("fetched invalid named remote %q", name)
		}
	}
	branchTestGit(t, dir, "remote", "add", "broken", filepath.Join(t.TempDir(), "missing.git"))
	if err := FetchGitRemote(context.Background(), dir, "broken"); err == nil || !strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("expected Git fetch diagnostic, got %v", err)
	}
	after, err := os.ReadFile(global)
	if err != nil || string(before) != string(after) {
		t.Fatalf("fetch changed global config: %v", err)
	}
}

func TestGitBranchesFetchCancellation(t *testing.T) {
	dir := branchTestRepo(t)
	branchTestGit(t, dir, "remote", "add", "origin", dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := FetchGitRemote(ctx, dir, "origin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	if err := FetchGitRemote(ctx, dir, "origin"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
	if refs := branchTestGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/remotes/"); refs != "" {
		t.Fatalf("canceled fetch updated refs: %q", refs)
	}
}

func TestGitBranchesFakeRunnerCompatibility(t *testing.T) {
	f := &fakeGit{responses: map[string]string{
		"rev-parse --is-inside-work-tree": "true",
		"symbolic-ref --short HEAD":       "main",
		"remote":                          "team\nteam/origin",
		"for-each-ref " + gitBranchesFormat + " refs/heads/ refs/remotes/": "refs/heads/main\t \trefs/remotes/team/main\t[ahead 2, behind 3]\t\nrefs/remotes/team/origin/topic/path\t \t\t\t\nrefs/remotes/team/HEAD\t \t\t\trefs/remotes/team/main",
	}}
	info := loadGit("/repo", f.run, okLook, gitEnv{})
	if len(info.Branches) != 2 {
		t.Fatalf("branches = %+v", info.Branches)
	}
	if b := info.Branches[0]; !b.Current || b.Ahead != 2 || b.Behind != 3 {
		t.Fatalf("local = %+v", b)
	}
	if b := info.Branches[1]; b.Remote != "team/origin" || b.Name != "topic/path" {
		t.Fatalf("remote = %+v", b)
	}
}
