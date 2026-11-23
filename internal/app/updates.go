package app

import (
	"context"
	"fmt"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

// updateKey identifies an update row. Versions are unique per SDK on a
// normal machine, and a duplicate install of the same version shares the
// same release line anyway.
func updateKey(kind core.Kind, version string) string {
	return string(kind) + "@" + version
}

// updateFor returns the last known status of an installed version.
func (a *App) updateFor(inst core.Installed) (core.Update, bool) {
	u, ok := a.updates[updateKey(inst.Kind, inst.Version)]
	return u, ok
}

// updateCount separates "a newer release of this line exists" from "this
// line is no longer maintained", because only the first is fixable by an
// upgrade.
func (a *App) updateCount() (outdated, ended int) {
	for _, u := range a.updates {
		if u.Outdated() {
			outdated++
		}
		if u.EOLState == core.EOLEnded {
			ended++
		}
	}
	return outdated, ended
}

// upgradableCount is how many versions EnvMgr could upgrade itself.
func (a *App) upgradableCount() int {
	n := 0
	for _, u := range a.updates {
		if u.Outdated() && u.Manager == "envmgr" && u.Kind != core.Rust {
			n++
		}
	}
	return n
}

// loadUpdates asks the sources how the installed versions stand. It is a
// background call: the pages read the last answer and never wait for HTTP.
func (a *App) loadUpdates(force bool) {
	if a.svc == nil || a.updatesBusy {
		return
	}
	a.updatesBusy = true
	a.updatesErr = ""
	post := a.jobDispatcher()
	svc := a.svc
	go func() {
		list, err := svc.Updates(context.Background(), force)
		post(func() {
			a.updatesBusy = false
			a.updatesDone = true
			a.updatesAt = nowLabel()
			next := make(map[string]core.Update, len(list))
			for _, u := range list {
				next[updateKey(u.Kind, u.Version)] = u
			}
			a.updates = next
			if err != nil {
				a.updatesErr = err.Error()
			}
		})
	}()
}

// startUpgrade installs the newest release of one version's line and says
// what happened to the projects that pinned the old one.
func (a *App) startUpgrade(u core.Update) {
	svc := a.svc
	if svc == nil {
		return
	}
	label := "升级 " + core.Info(u.Kind).Name + " " + u.Version
	job := a.startJob("upgrade:"+updateKey(u.Kind, u.Version), label, "准备", func(ctx context.Context, emit func(core.ReadyEvent)) (string, error) {
		inst, found := core.Exact(svc.Installed(), u.Kind, u.Version)
		if !found {
			return "", fmt.Errorf("本机已经没有 %s %s", core.Info(u.Kind).Name, u.Version)
		}
		target, err := svc.UpgradeTarget(ctx, inst)
		if err != nil {
			return "", err
		}
		ctx = core.WithInstallStage(ctx, func(stage string) {
			emit(core.ReadyEvent{Kind: u.Kind, Version: target.Version, Stage: stage})
		})
		now, err := svc.Install(ctx, target, func(done, total int64) {
			emit(core.ReadyEvent{Kind: u.Kind, Version: target.Version, Done: done, Total: total})
		})
		if err != nil {
			return "", err
		}
		detail := "已升级到 " + now.Version
		if users := svc.VersionUsers(u.Kind, u.Version); len(users) > 0 {
			detail += fmt.Sprintf("；%d 个项目仍固定 %s", len(users), u.Version)
		}
		return detail, nil
	})
	// The row badge and the counts are stale the moment the new version
	// lands, so refresh them once the job settles.
	go func() {
		if job == nil || job.workerDone == nil {
			return
		}
		<-job.workerDone
		a.jobDispatcher()(func() { a.loadUpdates(false) })
	}()
}

// updatesBadge is the per-row state of a version: outdated, unsupported, or
// nothing at all.
func (a *App) updatesBadge(c *ui.Context, inst core.Installed) {
	t := c.Theme()
	u, ok := a.updateFor(inst)
	if !ok {
		return
	}
	switch {
	case u.EOLState == core.EOLEnded:
		a.chip(c, "系列已停止维护", t.Danger, t.Danger.Alpha(0.12))
	case u.EOLState == core.EOLSoon && u.DaysLeft > 0:
		a.chip(c, fmt.Sprintf("%d 天后停止维护", u.DaysLeft), t.Warning, t.Warning.Alpha(0.12))
	}
}
