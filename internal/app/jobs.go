package app

import (
	"context"
	"errors"
	"fmt"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

type JobStatus string

const (
	JobRunning      JobStatus = "进行中"
	JobCompleted    JobStatus = "已完成"
	JobFailed       JobStatus = "失败"
	JobCancelled    JobStatus = "已取消"
	jobHistoryLimit           = 50
)

// taskService isolates background operations from the UI and from real installs.
type taskService interface {
	Install(context.Context, core.Remote, core.Progress) (core.Installed, error)
	InstallManager(context.Context, string, core.Progress) (core.ManagerState, error)
	ReadyProject(context.Context, core.Project, core.ReadyProgress) (core.ReadyResult, error)
}

type jobWork func(context.Context, func(core.ReadyEvent)) (string, error)

func (a *App) taskBackend() taskService {
	if a.tasks != nil {
		return a.tasks
	}
	return a.svc
}

// Capture the dispatcher before launching work; workers never read window state.
func (a *App) jobDispatcher() func(func()) {
	if a.taskUpdate != nil {
		return a.taskUpdate
	}
	if a.win != nil {
		return a.win.Update
	}
	return func(fn func()) { fn() }
}

func (a *App) startJob(id, label, stage string, run jobWork) *Job {
	if current := a.jobFor(id); current != nil && current.Running {
		return current
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{ID: id, Label: label, Stage: stage, Running: true, Status: JobRunning, cancel: cancel, run: run, initialStage: stage, workerDone: make(chan struct{})}
	a.jobs = append(a.jobs, job)
	notify := a.jobDispatcher()
	go func() {
		defer close(job.workerDone)
		detail, err := run(ctx, func(event core.ReadyEvent) {
			notify(func() {
				if !job.Running || job.Cancelling {
					return
				}
				if event.Stage != "" && event.Stage != job.Stage {
					job.Stage = event.Stage
					job.Done, job.Total = 0, 0
				}
				job.Done, job.Total = event.Done, event.Total
				job.Detail = describeProgress(event.Done, event.Total)
				if event.Kind != "" {
					job.Detail = core.Info(event.Kind).Name + " " + event.Version + " · " + job.Detail
				}
			})
		})
		cancelled := ctx.Err() != nil || errors.Is(err, context.Canceled)
		cancel()
		notify(func() { a.finishJob(job, detail, err, cancelled) })
	}()
	return job
}

func (a *App) finishJob(job *Job, detail string, err error, cancelled bool) {
	job.Running, job.Cancelling, job.cancel = false, false, nil
	job.Err = err
	switch {
	case cancelled:
		job.Status = JobCancelled
		if job.Err == nil {
			job.Err = context.Canceled
		}
	case err != nil:
		job.Status = JobFailed
	default:
		job.Status = JobCompleted
		job.Detail = detail
		if a.svc != nil {
			a.syncAndRecompute()
		}
		a.remoteList = ui.ListState{}
	}
	// Order terminal jobs by completion; keep all active jobs and the latest 50 results.
	for i, item := range a.jobs {
		if item == job {
			a.jobs = append(a.jobs[:i], a.jobs[i+1:]...)
			break
		}
	}
	a.jobs = append(a.jobs, job)
	remaining := jobHistoryLimit
	for i := len(a.jobs) - 1; i >= 0; i-- {
		if a.jobs[i].Running {
			continue
		}
		remaining--
		if remaining < 0 {
			a.jobs = append(a.jobs[:i], a.jobs[i+1:]...)
		}
	}
	message := job.Label + " · " + string(job.Status)
	if job.Err != nil && job.Status == JobFailed {
		message += "：" + job.Err.Error()
	}
	a.pushToast(message)
}

// Shutdown runs on the UI thread before quitting. Workers complete their disk
// cleanup independently of posted UI updates, so waiting does not block cleanup.
func (a *App) Shutdown(ctx context.Context) error {
	if a.codeStatsCancel != nil {
		a.codeStatsCancel()
	}
	for _, job := range a.jobs {
		a.cancelJob(job)
	}
	for _, job := range a.jobs {
		if job.workerDone == nil {
			continue
		}
		select {
		case <-job.workerDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (a *App) cancelJob(job *Job) {
	if job == nil || !job.Running || job.Cancelling || job.cancel == nil {
		return
	}
	job.Cancelling = true
	job.cancel()
}

func (a *App) retryJob(job *Job) {
	if job == nil || job.Running || job.run == nil || (job.Status != JobFailed && job.Status != JobCancelled) {
		return
	}
	a.startJob(job.ID, job.Label, job.initialStage, job.run)
}

func (a *App) startInstall(r core.Remote) {
	svc := a.taskBackend()
	a.startJob(remoteJobID(r), "安装 "+core.Info(r.Kind).Name+" "+r.Version, "下载", func(ctx context.Context, emit func(core.ReadyEvent)) (string, error) {
		ctx = core.WithInstallStage(ctx, func(stage string) { emit(core.ReadyEvent{Kind: r.Kind, Version: r.Version, Stage: stage}) })
		inst, err := svc.Install(ctx, r, func(done, total int64) {
			emit(core.ReadyEvent{Kind: r.Kind, Version: r.Version, Done: done, Total: total})
		})
		return "已安装 " + core.Info(inst.Kind).Name + " " + inst.Version, err
	})
}

func (a *App) installManager(m core.ManagerState) {
	svc := a.taskBackend()
	a.startJob("manager:"+m.ID, "安装 "+m.Name, "下载", func(ctx context.Context, emit func(core.ReadyEvent)) (string, error) {
		ctx = core.WithInstallStage(ctx, func(stage string) { emit(core.ReadyEvent{Stage: stage}) })
		_, err := svc.InstallManager(ctx, m.ID, func(done, total int64) { emit(core.ReadyEvent{Done: done, Total: total}) })
		return m.Name + " 已就绪", err
	})
}

func projectJobID(p core.Project) string {
	if p.ID != "" {
		return "ready:" + p.ID
	}
	return "ready:" + p.Path
}

func (a *App) readyProject(p core.Project) {
	svc := a.taskBackend()
	a.startJob(projectJobID(p), "就绪项目 "+p.Name, "安装", func(ctx context.Context, emit func(core.ReadyEvent)) (string, error) {
		result, err := svc.ReadyProject(ctx, p, core.ReadyProgress(emit))
		return fmt.Sprintf("已安装 %d 个 SDK，已验证 %d 个 SDK", len(result.Installed), len(result.Verified)), err
	})
}

func (a *App) jobRow(c *ui.Context, job *Job) {
	t := c.Theme()
	ui.Column(c).Gap(5).Padding(8, 10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, job.Label).FontSize(12).Bold().Grow(1).SingleLine().Ellipsis("…")
			status := string(job.Status) + " · " + job.Stage
			if job.Running {
				status = job.Stage
			}
			if job.Cancelling {
				status = "正在取消"
			}
			ui.Text(c, status).FontSize(11).TextColor(t.TextMuted)
			if job.Running {
				if !job.Cancelling && ui.Button(c, "取消").Label("取消 "+job.Label).Clicked() {
					a.cancelJob(job)
				}
			} else if job.Status == JobFailed || job.Status == JobCancelled {
				if ui.Button(c, "重试").Label("重试 " + job.Label).Clicked() {
					a.retryJob(job)
				}
			}
		})
		if job.Detail != "" {
			ui.Text(c, job.Detail).FontSize(10.5).TextColor(t.TextMuted).Wrap()
		}
		if job.Err != nil {
			ui.Text(c, job.Err.Error()).FontSize(11).TextColor(t.Danger).Wrap()
		}
		if job.Running {
			ui.Progress(c, job.Progress()).Label(job.Label + "进度")
		}
	})
}

func (a *App) downloadJobs(c *ui.Context) {
	if len(a.jobs) == 0 {
		return
	}
	a.card(c).MaxHeight(230).Gap(5).Padding(8).Children(func() {
		ui.Text(c, "任务与历史 · 最近 50 条").FontSize(12).Bold()
		ui.List(c, &a.jobList, len(a.jobs), func(i int) { a.jobRow(c, a.jobs[len(a.jobs)-1-i]) }).Height(180)
	})
}
