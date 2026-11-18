package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

func gitWorkspaceFixture(t *testing.T) (*App, *ui.Tester, string, chan func()) {
	t.Helper()
	a := newTestApp(t)
	home := t.TempDir()
	global := filepath.Join(home, "global.gitconfig")
	if err := os.WriteFile(global, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "UI Test")
	git("config", "user.email", "ui@example.test")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "initial")
	git("branch", "develop")
	git("remote", "add", "github", "https://github.com/team/project.git")
	git("remote", "add", "gitee", "https://gitee.com/team/project.git")
	git("update-ref", "refs/remotes/github/from-github", "HEAD")
	git("update-ref", "refs/remotes/gitee/from-gitee", "HEAD")
	if err := core.SetGitDefaultRemote(dir, "github"); err != nil {
		t.Fatal(err)
	}
	p := core.NewProject(dir)
	p.Name = "Git UI Fixture"
	a.projects = []core.Project{p}
	a.resolutions = map[string][]core.Resolution{}
	a.page = PageProjects
	a.OpenProject(0, false)
	_ = a.svc.Git(dir)
	a.PrepareProjectStats()
	updates := make(chan func(), 8)
	a.watchService(func(apply func()) { updates <- apply }, func() {})
	return a, ui.NewTester(a.View, 1180, 1200), dir, updates
}

func TestGitWorkspaceSelectorsBranchesAndDialogs(t *testing.T) {
	a, tt, dir, updates := gitWorkspaceFixture(t)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	finish := func() {
		t.Helper()
		if !a.gitBusy {
			t.Fatalf("selection did not start its Git operation: remote=%+v branch=%+v selected=%s error=%s", a.gitRemotePick, a.gitBranchPick, a.gitSelectedRemote, a.gitError)
		}
		for i := 0; i < 8; i++ {
			awaitUpdate(t, updates)()
			if !a.gitBusy {
				tt.Frame()
				return
			}
		}
		t.Fatal("Git action did not finish")
	}
	// The order of the actual rendered sections is part of the redesign.
	lastY := float32(-1)
	for _, label := range []string{"远程仓库", "分支与改动", "提交身份", "项目代码统计"} {
		r, ok := tt.Find(label)
		if !ok || r.Y <= lastY {
			t.Fatalf("section order/visibility failed at %q: %+v", label, r)
		}
		lastY = r.Y
	}
	if a.gitRemotePick.Name != "github" || a.gitRemotePick.Value != "https://github.com/team/project.git" {
		t.Fatalf("alias/address selection = %+v", a.gitRemotePick)
	}
	if a.codeStats.Files != 1 || a.codeStats.CodeLines != 2 || a.codeStats.BlankLines != 1 {
		t.Fatalf("prepared code statistics = %+v", a.codeStats)
	}

	click("Git 远程仓库")
	click("gitee")
	finish()
	if actual := core.LoadGit(dir); actual.PushRemote != "gitee" {
		t.Fatalf("effective remote = %s", actual.PushRemote)
	}
	if a.gitRemotePick.Name != "gitee" {
		t.Fatalf("selected alias = %+v", a.gitRemotePick)
	}
	click("Git 分支")
	if !tt.HasText("gitee/from-gitee") || tt.HasText("github/from-github") {
		t.Fatal("branch popup is not filtered by selected remote")
	}
	click("develop")
	finish()
	if actual := core.LoadGit(dir); actual.Branch != "develop" {
		t.Fatalf("switch branch = %s", actual.Branch)
	}

	click("新建分支")
	click("新分支名称")
	tt.Type("feature/ui-test")
	click("创建并切换")
	finish()
	if a.gitBranchCreate || core.LoadGit(dir).Branch != "feature/ui-test" {
		t.Fatal("create did not switch branch and close dialog")
	}
	click("Git 分支")
	click("main")
	finish()
	click("删除分支")
	click("要删除的分支")
	click("feature/ui-test")
	click("确认删除本地分支")
	finish()
	for _, branch := range core.LoadGit(dir).Branches {
		if branch.Remote == "" && branch.Name == "feature/ui-test" {
			t.Fatal("chosen existing branch was not deleted")
		}
	}

	click("新建分支")
	click("新分支名称")
	tt.Type("bad name")
	click("创建并切换")
	finish()
	if !a.gitBranchCreate || a.gitBranchName != "bad name" || a.gitError == "" {
		t.Fatal("failed branch action did not retain input/error")
	}
	click("取消")
	burstWheel(tt, 120)
	burstWheel(tt, -120)
	if tt.FramePending() {
		t.Fatal("redesigned Git card did not settle after wheel bursts")
	}
}

func TestGitBranchChoicesKeepValuesAndExcludeOtherRemotes(t *testing.T) {
	info := core.GitInfo{Branch: "main", Branches: []core.GitBranch{
		{Name: "main", Ref: "refs/heads/main", Current: true},
		{Name: "feature/one", Ref: "refs/remotes/github/feature/one", Remote: "github"},
		{Name: "feature/two", Ref: "refs/remotes/gitee/feature/two", Remote: "gitee"},
	}}
	choices := gitBranchChoices(info, "github")
	if len(choices) != 2 || choices[0].Value != "refs/heads/main" || choices[1].Value != "refs/remotes/github/feature/one" {
		t.Fatalf("branch choices = %+v", choices)
	}
	a := &App{gitInfo: info}
	for _, name := range a.deletableGitBranches() {
		if name == "main" || strings.Contains(name, "feature/") {
			t.Fatalf("deletion offered current or remote branch %q", name)
		}
	}
	unborn := gitBranchChoices(core.GitInfo{Branch: "main"}, "")
	if len(unborn) != 1 || unborn[0].Value != "refs/heads/main" {
		t.Fatalf("unborn branch = %+v", unborn)
	}
}
