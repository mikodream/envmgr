package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

type fakeTaskService struct {
	install func(context.Context, core.Remote, core.Progress) (core.Installed, error)
	manager func(context.Context, string, core.Progress) (core.ManagerState, error)
	ready   func(context.Context, core.Project, core.ReadyProgress) (core.ReadyResult, error)
}

func (f fakeTaskService) Install(ctx context.Context, r core.Remote, progress core.Progress) (core.Installed, error) {
	return f.install(ctx, r, progress)
}
func (f fakeTaskService) InstallManager(ctx context.Context, id string, progress core.Progress) (core.ManagerState, error) {
	return f.manager(ctx, id, progress)
}
func (f fakeTaskService) ReadyProject(ctx context.Context, p core.Project, progress core.ReadyProgress) (core.ReadyResult, error) {
	return f.ready(ctx, p, progress)
}

// Dispatch worker updates on the test thread, just like Window.Update does.
func taskTestApp() (*App, chan func()) {
	updates := make(chan func(), 256)
	a := &App{themeMode: "light", page: PageDownloads, taskUpdate: func(fn func()) { updates <- fn }}
	a.SetRemote(core.Node, []core.Remote{{Kind: core.Node, Version: "24.0.0"}})
	a.downloadKind = kindIndexOf(core.Node)
	return a, updates
}

func awaitTask(t *testing.T, updates <-chan func(), done func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case update := <-updates:
			update()
		case <-timer.C:
			t.Fatal("timed out waiting for task update")
		}
	}
}

func TestInstallTaskCancelDeduplicatesAndRetries(t *testing.T) {
	a, updates := taskTestApp()
	r := core.Remote{Kind: core.Node, Version: "24.0.0"}
	calls := make(chan struct{}, 3)
	a.tasks = fakeTaskService{install: func(ctx context.Context, got core.Remote, progress core.Progress) (core.Installed, error) {
		calls <- struct{}{}
		progress(32, 100)
		<-ctx.Done()
		return core.Installed{}, fmt.Errorf("download interrupted: %w", ctx.Err())
	}}
	a.startInstall(r)
	first := a.jobFor(remoteJobID(r))
	a.startInstall(r)
	if len(a.jobs) != 1 {
		t.Fatal("duplicate install was started")
	}
	awaitTask(t, updates, func() bool { return first.Done == 32 })
	if first.Stage != "下载" || first.Progress() != 0.32 {
		t.Fatalf("progress: %+v", first)
	}
	tt := ui.NewTester(a.View, 1180, 760)
	if err := tt.Click("取消 " + first.Label); err != nil {
		t.Fatal(err)
	}
	if !first.Cancelling || !first.Running {
		t.Fatal("cancel must wait for worker before allowing another install")
	}
	a.startInstall(r)
	if len(a.jobs) != 1 {
		t.Fatal("cancelling task allowed duplicate")
	}
	awaitTask(t, updates, func() bool { return !first.Running })
	if first.Status != JobCancelled || !errors.Is(first.Err, context.Canceled) || first.Done != 32 {
		t.Fatalf("cancel result: %+v", first)
	}
	tt = ui.NewTester(a.View, 1180, 760)
	if !tt.HasText("download interrupted: context canceled") {
		t.Fatal("cancel error detail disappeared")
	}
	if err := tt.Click("重试 " + first.Label); err != nil {
		t.Fatal(err)
	}
	second := a.jobFor(remoteJobID(r))
	if second == first || !second.Running || len(a.jobs) != 2 {
		t.Fatal("retry must create a fresh attempt and retain prior result")
	}
	a.retryJob(first)
	if len(a.jobs) != 2 {
		t.Fatal("repeated retry started a duplicate")
	}
	a.cancelJob(second)
	awaitTask(t, updates, func() bool { return !second.Running })
	if len(calls) != 2 {
		t.Fatalf("install calls=%d", len(calls))
	}
}

func TestManagerFailureRetainsDetailAndRetrySucceeds(t *testing.T) {
	a, updates := taskTestApp()
	attempt := 0
	a.tasks = fakeTaskService{manager: func(ctx context.Context, id string, progress core.Progress) (core.ManagerState, error) {
		attempt++
		progress(17, 20)
		if attempt == 1 {
			return core.ManagerState{}, errors.New("checksum mismatch: expected abc, got def")
		}
		return core.ManagerState{ID: id}, nil
	}}
	m := core.ManagerState{ID: "fnm", Name: "fnm"}
	a.installManager(m)
	first := a.jobFor("manager:fnm")
	a.installManager(m)
	awaitTask(t, updates, func() bool { return !first.Running })
	if first.Status != JobFailed || first.Err == nil || first.Done != 17 {
		t.Fatalf("failure lost data: %+v", first)
	}
	tt := ui.NewTester(a.View, 1180, 760)
	if !tt.HasText(first.Err.Error()) {
		t.Fatal("full error is absent from task history")
	}
	if err := tt.Click("重试 " + first.Label); err != nil {
		t.Fatal(err)
	}
	second := a.jobFor(first.ID)
	awaitTask(t, updates, func() bool { return !second.Running })
	if second.Status != JobCompleted || second.Err != nil || len(a.jobs) != 2 || attempt != 2 {
		t.Fatalf("retry: %+v attempts=%d", second, attempt)
	}
	if first.Status != JobFailed {
		t.Fatal("retry modified previous failure")
	}
}

func TestReadyProjectStageFailureAndRetry(t *testing.T) {
	a, updates := taskTestApp()
	p := core.Project{ID: "project", Name: "demo", Path: "/offline/demo", Pins: map[core.Kind]string{core.Node: "24"}}
	seen := make(chan core.Project, 2)
	attempt := 0
	a.tasks = fakeTaskService{ready: func(ctx context.Context, got core.Project, progress core.ReadyProgress) (core.ReadyResult, error) {
		seen <- got
		attempt++
		for _, stage := range []string{"下载", "校验", "解包", "安装", "验证"} {
			progress(core.ReadyEvent{Kind: core.Node, Version: "24.0.0", Stage: stage, Done: 3, Total: 4})
		}
		if attempt == 1 {
			return core.ReadyResult{}, errors.New("node --version: executable exited 1")
		}
		return core.ReadyResult{Installed: []core.Installed{{Kind: core.Node}}, Verified: []core.VerifiedSDK{{}}}, nil
	}}
	a.readyProject(p)
	first := a.jobFor(projectJobID(p))
	a.readyProject(p)
	awaitTask(t, updates, func() bool { return !first.Running })
	if first.Stage != "验证" || first.Status != JobFailed || !strings.Contains(first.Detail, "24.0.0") {
		t.Fatalf("ready result: %+v", first)
	}
	a.retryJob(first)
	second := a.jobFor(first.ID)
	awaitTask(t, updates, func() bool { return !second.Running })
	if second.Status != JobCompleted || second.Detail != "已安装 1 个 SDK，已验证 1 个 SDK" {
		t.Fatalf("ready retry: %+v", second)
	}
	if got := <-seen; got.ID != p.ID || got.Pins[core.Node] != "24" {
		t.Fatalf("wrong project: %+v", got)
	}
	if len(seen) != 1 {
		t.Fatal("duplicate ready call")
	}
}

func TestProjectDetailReadyButtonAndCancellation(t *testing.T) {
	a := newTestApp(t)
	updates := make(chan func(), 32)
	a.taskUpdate = func(fn func()) { updates <- fn }
	p := core.NewProject(t.TempDir())
	p.Name = "offline-project"
	a.projects = []core.Project{p}
	_ = a.svc.Git(p.Path)
	a.tasks = fakeTaskService{ready: func(ctx context.Context, got core.Project, progress core.ReadyProgress) (core.ReadyResult, error) {
		progress(core.ReadyEvent{Kind: core.Python, Version: "3.13.0", Stage: "校验"})
		<-ctx.Done()
		return core.ReadyResult{}, ctx.Err()
	}}
	a.page = PageProjects
	a.OpenProject(0, false)
	tt := ui.NewTester(a.View, 1180, 780)
	if err := tt.Click("一键就绪"); err != nil {
		t.Fatal(err)
	}
	job := a.jobFor(projectJobID(p))
	if job == nil {
		t.Fatal("project button did not start background task")
	}
	awaitTask(t, updates, func() bool { return job.Stage == "校验" })
	tt = ui.NewTester(a.View, 1180, 780)
	if !tt.HasText("正在就绪 · 校验") {
		t.Fatal("project progress is absent")
	}
	if err := tt.Click("取消就绪"); err != nil {
		t.Fatal(err)
	}
	awaitTask(t, updates, func() bool { return !job.Running })
	if job.Status != JobCancelled {
		t.Fatalf("project cancellation: %+v", job)
	}
}

func TestTaskHistoryKeepsLatestFiftyAndActive(t *testing.T) {
	a, updates := taskTestApp()
	active := a.startJob("active", "active", "下载", func(ctx context.Context, emit func(core.ReadyEvent)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	for i := 0; i < 55; i++ {
		job := a.startJob(fmt.Sprint(i), fmt.Sprint(i), "验证", func(context.Context, func(core.ReadyEvent)) (string, error) { return "ok", nil })
		awaitTask(t, updates, func() bool { return !job.Running })
	}
	if len(a.jobs) != 51 || a.jobFor("4") != nil || a.jobFor("5") == nil || a.jobFor("active") != active {
		t.Fatalf("history count=%d", len(a.jobs))
	}
	a.cancelJob(active)
	awaitTask(t, updates, func() bool { return !active.Running })
	if len(a.jobs) != 50 || a.jobFor("5") != nil || a.jobFor("active") == nil {
		t.Fatal("finishing active task did not trim terminal history")
	}
}

func TestShutdownCancelsAndWaitsForWorkerCleanup(t *testing.T) {
	a, updates := taskTestApp()
	started := make(chan struct{})
	cleaned := make(chan struct{})
	a.tasks = fakeTaskService{install: func(ctx context.Context, _ core.Remote, _ core.Progress) (core.Installed, error) {
		close(started)
		<-ctx.Done()
		close(cleaned)
		return core.Installed{}, ctx.Err()
	}}
	a.startInstall(core.Remote{Kind: core.Node, Version: "24.0.0"})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("quit returned before cleanup")
	}
	job := a.jobs[0]
	awaitTask(t, updates, func() bool { return !job.Running })
	if job.Status != JobCancelled {
		t.Fatalf("status=%s", job.Status)
	}
}
