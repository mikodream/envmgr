package app

import (
	"context"
	"fmt"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

// The disk card exists because an SDK manager that hides its footprint is a
// manager users stop installing into. Every version shows its own size, the
// download cache is a number rather than a mystery directory, and leftovers
// from an interrupted install are named instead of silently occupying space.

// DiskReady reports whether the footprint measurement has landed, so a
// snapshot or a test can wait for real numbers instead of the placeholder.
func (a *App) DiskReady() bool { return a.diskReady && !a.diskBusy }

// loadDisk starts the measurement unless the numbers are already here.
// Drawing calls it every frame, so it must be free once the answer exists.
func (a *App) loadDisk() { a.measureDisk(false) }

// remeasureDisk throws the numbers away and measures again, for the button.
func (a *App) remeasureDisk() {
	a.diskReady = false
	a.measureDisk(true)
}

// measureDisk walks every install in the background. The first run is a full
// directory walk, so the pages keep drawing and the numbers appear when ready.
func (a *App) measureDisk(force bool) {
	if a.svc == nil || a.diskBusy {
		return
	}
	if a.diskReady && !force {
		return
	}
	a.diskBusy = true
	post := a.jobDispatcher()
	svc := a.svc
	go func() {
		rep, err := svc.DiskReport()
		post(func() {
			a.diskBusy = false
			a.diskReady = true
			a.disk = rep
			next := make(map[string]int64, len(rep.Entries))
			for _, e := range rep.Entries {
				if e.Known {
					next[updateKey(e.Kind, e.Version)] = e.Size
				}
			}
			a.diskSizes = next
			a.diskErr = ""
			if err != nil {
				a.diskErr = err.Error()
			}
		})
	}()
}

// sizeOf returns the last measured size of an install, if it is known.
func (a *App) sizeOf(inst core.Installed) (int64, bool) {
	size, ok := a.diskSizes[updateKey(inst.Kind, inst.Version)]
	return size, ok && size > 0
}

// sizeChip shows one version's footprint in a list row.
func (a *App) sizeChip(c *ui.Context, inst core.Installed) {
	t := c.Theme()
	size, ok := a.sizeOf(inst)
	if !ok {
		return
	}
	a.chip(c, core.HumanSize(size), t.TextMuted, t.SurfaceHover)
}

// cleanDisk reclaims the cache and/or residue. Installed versions are never
// part of it: those go through the delete button, which knows which projects
// still pin them.
func (a *App) cleanDisk(cache, orphan bool) {
	svc := a.svc
	if svc == nil {
		return
	}
	label := "清理磁盘"
	switch {
	case cache && orphan:
		label = "清理缓存与残留"
	case cache:
		label = "清理下载缓存"
	case orphan:
		label = "清理残留目录"
	}
	job := a.startJob("disk:clean", label, "清理", func(ctx context.Context, emit func(core.ReadyEvent)) (string, error) {
		res, err := svc.CleanDisk(core.CleanOptions{Cache: cache, Residue: orphan})
		if err != nil {
			return core.DescribeClean(res), err
		}
		return core.DescribeClean(res), nil
	})
	// The numbers on screen are wrong the moment files disappear.
	go func() {
		if job == nil || job.workerDone == nil {
			return
		}
		<-job.workerDone
		a.jobDispatcher()(func() { a.remeasureDisk() })
	}()
}

// diskCard renders the footprint and the two clean-up actions.
func (a *App) diskCard(c *ui.Context) {
	// The card can be reached before SetWindow ran (snapshot tools, tests);
	// drawing it starts the measurement rather than waiting forever.
	a.loadDisk()
	t := c.Theme()
	card := a.card(c).Gap(10)
	card.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "磁盘占用").FontSize(13).Bold()
			ui.Spacer(c)
			if a.diskBusy {
				a.chip(c, "统计中…", t.TextMuted, t.SurfaceHover)
			} else if a.diskReady {
				if ui.Button(c, "重新统计").Clicked() {
					a.remeasureDisk()
				}
			}
		})
		if !a.diskReady {
			ui.Text(c, "第一次统计要逐个目录计算大小，完成后这里会显示每个版本占用的空间。").
				FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(560)
			return
		}
		if a.diskErr != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icAlert).Size(12, 12).TextColor(t.Warning)
				ui.Text(c, a.diskErr).FontSize(10.5).TextColor(t.Warning).Wrap().MaxWidth(520)
			})
		}

		a.diskLine(c, "已安装 SDK", core.HumanSize(a.disk.Total), fmt.Sprintf(
			"%d 个版本 · EnvMgr 管理 %s · 其他管理器 %s",
			len(a.disk.Entries), core.HumanSize(a.disk.Managed), core.HumanSize(a.disk.External)))

		for _, kind := range core.Kinds {
			var size int64
			count, known := 0, 0
			for _, e := range a.disk.Entries {
				if e.Kind != kind {
					continue
				}
				count++
				if e.Known {
					known++
					size += e.Size
				}
			}
			if count == 0 {
				continue
			}
			// Every install of this kind may sit in a shared system directory,
			// which the report deliberately does not count.
			value := core.HumanSize(size)
			if known == 0 {
				value = "—"
			}
			a.diskLine(c, "  "+core.Info(kind).Name, value, fmt.Sprintf("%d 个", count))
		}

		// Cache: kept so a reinstall needs no network, pruned on request.
		cacheNote := "空"
		if a.disk.CacheFiles > 0 {
			cacheNote = fmt.Sprintf("%d 个归档", a.disk.CacheFiles)
			if !a.disk.CacheOldest.IsZero() {
				cacheNote += "，最早 " + a.disk.CacheOldest.Format("2006-01-02")
			}
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "下载缓存").FontSize(12).Width(120).Shrink(0)
			ui.Text(c, core.HumanSize(a.disk.CacheSize)).FontSize(12).Bold().Width(80).Shrink(0)
			ui.Text(c, cacheNote).FontSize(10.5).TextColor(t.TextMuted).Grow(1).SingleLine().Ellipsis("…")
			if a.disk.CacheFiles > 0 {
				if ui.Button(c, "清理").Label("清除下载缓存，已安装版本不受影响").Clicked() {
					a.cleanDisk(true, false)
				}
			}
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "残留").FontSize(12).Width(120).Shrink(0)
			ui.Text(c, core.HumanSize(a.disk.ResidueSize)).FontSize(12).Bold().Width(80).Shrink(0)
			if len(a.disk.Residue) == 0 {
				ui.Text(c, "没有发现中断安装留下的目录").FontSize(10.5).TextColor(t.TextMuted).Grow(1).SingleLine()
				return
			}
			ui.Text(c, fmt.Sprintf("%d 个目录，占不上任何版本", len(a.disk.Residue))).
				FontSize(10.5).TextColor(t.TextMuted).Grow(1).SingleLine().Ellipsis("…")
			if ui.Button(c, "清理").Label("删除未登记的残留目录").Clicked() {
				a.cleanDisk(false, true)
			}
		})

		if len(a.disk.Residue) > 0 {
			for i, r := range a.disk.Residue {
				if i >= 3 {
					ui.Text(c, fmt.Sprintf("…还有 %d 个", len(a.disk.Residue)-3)).
						FontSize(10).TextColor(t.TextMuted)
					break
				}
				ui.Text(c, "  "+core.HumanSize(r.Size)+"  "+shortPath(r.Path)+"  "+r.Label).
					FontSize(10).Font("monospace").TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			}
		}

		ui.Text(c, "清理只删除下载归档和残留目录；已安装的版本一个都不会动，刚写入不到 10 分钟的文件也会自动跳过。").
			FontSize(10).TextColor(t.TextMuted).Wrap().MaxWidth(560)
	})
}

// diskLine is one labelled number with a muted explanation beside it.
func (a *App) diskLine(c *ui.Context, label, value, note string) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).Width(120).Shrink(0).SingleLine().Ellipsis("…")
		ui.Text(c, value).FontSize(12).Bold().Width(80).Shrink(0)
		ui.Text(c, note).FontSize(10.5).TextColor(t.TextMuted).Grow(1).SingleLine().Ellipsis("…")
	})
}
