package app

import (
	"context"
	"fmt"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

// PrepareProjectStats warms the offline snapshot tool before its first frame.
// A native window always uses the cancellable background path below.
func (a *App) PrepareProjectStats() {
	rows := a.projectRows()
	if !a.projectOpen || a.selProject < 0 || a.selProject >= len(rows) {
		return
	}
	dir := rows[a.selProject].Path
	a.codeStats = core.LoadCodeStats(context.Background(), dir)
	a.codeStatsFor = dir
	a.codeStatsReady = true
	a.codeStatsLoading = false
}

func (a *App) loadProjectCodeStats(dir string, force bool) {
	if a.codeStatsFor == dir && (a.win == nil || (!force && (a.codeStatsLoading || a.codeStatsReady))) {
		return
	}
	if a.codeStatsCancel != nil {
		a.codeStatsCancel()
	}
	a.codeStatsGeneration++
	generation := a.codeStatsGeneration
	if a.codeStatsFor != dir {
		a.codeStats = core.CodeStats{}
		a.codeStatsReady = false
	}
	a.codeStatsFor = dir
	if a.win == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.codeStatsCancel = cancel
	a.codeStatsLoading = true
	win := a.win
	go func() {
		defer cancel()
		stats := core.LoadCodeStats(ctx, dir)
		win.Update(func() {
			if generation != a.codeStatsGeneration || dir != a.codeStatsFor {
				return
			}
			a.codeStatsLoading = false
			a.codeStatsReady = true
			a.codeStats = stats
		})
	}()
}

func (a *App) gitCodeStats(c *ui.Context, p core.Project) {
	a.loadProjectCodeStats(p.Path, false)
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "项目代码统计").FontSize(11.5).Bold()
		ui.Spacer(c)
		if a.codeStatsLoading {
			ui.Text(c, "统计中…").FontSize(10.5).TextColor(t.TextMuted)
		}
		if a.button(c, icRefresh, "重新统计").Disabled(a.codeStatsLoading).Clicked() {
			a.loadProjectCodeStats(p.Path, true)
		}
	})
	if !a.codeStatsReady {
		ui.Text(c, "在后台统计源代码，首次读取完成后显示结果。").FontSize(10.5).TextColor(t.TextMuted)
		return
	}
	stats := a.codeStats
	ui.Grid(c).Columns(4).Gap(8).Children(func() {
		a.gitMetric(c, "源文件", fmt.Sprint(stats.Files), t.Accent)
		a.gitMetric(c, "非空行", fmt.Sprint(stats.CodeLines), t.Success)
		a.gitMetric(c, "空白行", fmt.Sprint(stats.BlankLines), t.TextMuted)
		a.gitMetric(c, "代码体积", core.HumanSize(stats.Bytes), t.TextMuted)
	})
	if len(stats.Languages) > 0 {
		ui.Row(c).Gap(6).Wrap().Children(func() {
			for i, language := range stats.Languages {
				if i >= 6 {
					a.chip(c, fmt.Sprintf("+%d 种", len(stats.Languages)-6), t.TextMuted, t.SurfaceHover)
					break
				}
				a.chip(c, fmt.Sprintf("%s · %d 行", language.Language, language.CodeLines), t.TextMuted, t.SurfaceHover)
			}
		})
	}
	ui.Text(c, "非空行包含注释；Git 仓库遵循忽略规则，依赖与构建目录不计入。").FontSize(10).TextColor(t.TextMuted).Wrap().MaxWidth(640)
	if stats.Skipped > 0 {
		ui.Text(c, fmt.Sprintf("已排除或跳过 %d 个非源码、依赖或较大条目。", stats.Skipped)).FontSize(10).TextColor(t.TextMuted).Wrap()
	}
	if stats.Truncated {
		ui.Text(c, "统计达到扫描上限或已中断，当前显示部分结果。").FontSize(10).TextColor(t.Warning).Wrap()
	}
	if stats.Err != "" {
		ui.Text(c, stats.Err).FontSize(10).TextColor(t.Warning).Wrap().MaxWidth(640)
	}
}

func (a *App) gitMetric(c *ui.Context, label, value string, color ui.Color) {
	t := c.Theme()
	ui.Column(c).Gap(3).Padding(9, 10).Radius(8).Background(t.SurfaceHover).Children(func() {
		ui.Text(c, label).FontSize(10).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(16).Bold().TextColor(color).SingleLine()
	})
}
