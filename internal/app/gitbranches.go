package app

import (
	"context"
	"fmt"
	"strings"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

func gitBranchChoices(info core.GitInfo, remote string) []gitSelectChoice {
	choices := make([]gitSelectChoice, 0, len(info.Branches)+1)
	currentFound := false
	for _, branch := range info.Branches {
		if branch.Remote != "" && branch.Remote != remote {
			continue
		}
		name, detail := branch.Name, "本地分支"
		if branch.Remote != "" {
			name = branch.Remote + "/" + branch.Name
			detail = "远端分支 · 切换时创建本地跟踪分支"
		} else if branch.Upstream != "" {
			detail = "跟踪 " + branch.Upstream
		}
		if branch.Current {
			currentFound = true
		}
		choices = append(choices, gitSelectChoice{Name: name, Value: branch.Ref, Detail: detail})
	}
	if !currentFound && info.Branch != "" && !info.Detached {
		choices = append([]gitSelectChoice{{Name: info.Branch, Value: "refs/heads/" + info.Branch, Detail: "本地分支 · 尚无提交"}}, choices...)
	}
	return choices
}

func (a *App) gitBranchPanel(c *ui.Context, p core.Project) {
	t := c.Theme()
	selected, _ := a.selectedGitRemote()
	choices := gitBranchChoices(a.gitInfo, selected.Name)
	initial := gitSelectChoice{Name: "游离 HEAD", Detail: "选择分支以切换工作区"}
	var current core.GitBranch
	for _, branch := range a.gitInfo.Branches {
		if branch.Current {
			current = branch
			break
		}
	}
	for _, choice := range choices {
		if choice.Value == "refs/heads/"+a.gitInfo.Branch && !a.gitInfo.Detached {
			initial = choice
			break
		}
	}
	if !containsGitChoice(choices, a.gitBranchPick) {
		a.gitBranchPick = initial
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "分支与改动").FontSize(11.5).Bold()
		ui.Spacer(c)
		if ui.Button(c, "获取分支").Disabled(a.gitBusy || selected.Name == "").Clicked() {
			name := selected.Name
			a.runGitChange(p.Path, func() error { return core.FetchGitRemote(context.Background(), p.Path, name) }, "已更新 "+name+" 的远端分支")
		}
		if ui.Button(c, "新建分支").Disabled(a.gitBusy).Clicked() {
			a.gitBranchName = ""
			a.gitBranchCreate = true
			a.gitError = ""
		}
		if ui.Button(c, "删除分支").Disabled(a.gitBusy || len(a.deletableGitBranches()) == 0).Clicked() {
			a.gitBranchDeleteName = ""
			a.gitBranchDelete = true
			a.gitError = ""
		}
	})
	ui.Row(c).Gap(8).Children(func() {
		if a.gitValueSelect(c, &a.gitBranchPick, choices, "Git 分支") {
			ref := a.gitBranchPick.Value
			a.runGitChange(p.Path, func() error { return core.SwitchGitBranch(p.Path, ref) }, "已切换分支")
		}
	})
	ui.Grid(c).Columns(4).Gap(8).Children(func() {
		a.gitMetric(c, "已修改", fmt.Sprint(a.gitInfo.Modified), t.Warning)
		a.gitMetric(c, "已暂存", fmt.Sprint(a.gitInfo.Staged), t.Success)
		a.gitMetric(c, "未跟踪", fmt.Sprint(a.gitInfo.Untracked), t.Accent)
		divergence := "—"
		if current.Upstream != "" {
			divergence = fmt.Sprintf("%d / %d", current.Ahead, current.Behind)
		}
		a.gitMetric(c, "领先 / 落后", divergence, t.TextMuted)
	})
	text := "改动来自当前本地工作区；远端分支来自最近获取的引用。"
	if current.Upstream != "" {
		text = "当前跟踪 " + current.Upstream + "；领先/落后基于本地已获取的引用。"
	}
	ui.Text(c, text).FontSize(10).TextColor(t.TextMuted).Wrap().MaxWidth(640)
	if a.gitInfo.Root != "" && !samePath(a.gitInfo.Root, p.Path) {
		ui.Text(c, "仓库根目录 "+a.gitInfo.Root).FontSize(10).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
	}
}

func (a *App) deletableGitBranches() []string {
	var names []string
	for _, branch := range a.gitInfo.Branches {
		if branch.Remote == "" && !branch.Current {
			names = append(names, branch.Name)
		}
	}
	return names
}

func (a *App) gitBranchDialogs(c *ui.Context) {
	if a.gitBranchCreate {
		open := true
		ui.Modal(c, &open, func() {
			ui.Text(c, "新建分支").FontSize(14).Bold()
			ui.Text(c, "从当前分支新建，并切换到新分支。").FontSize(11).TextColor(c.Theme().TextMuted)
			ui.Text(c, "分支名称").FontSize(11)
			ui.TextInput(c, &a.gitBranchName).Label("新分支名称").Placeholder("feature/my-change")
			if a.gitError != "" {
				ui.Text(c, a.gitError).FontSize(10.5).TextColor(c.Theme().Danger).Wrap().MaxWidth(440)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.gitBranchCreate = false
				}
				if ui.PrimaryButton(c, "创建并切换").Disabled(a.gitBusy || strings.TrimSpace(a.gitBranchName) == "").Clicked() {
					dir, name := a.gitFor, strings.TrimSpace(a.gitBranchName)
					a.runGitChange(dir, func() error { return core.CreateGitBranch(dir, name) }, "已创建并切换到 "+name, func() { a.gitBranchCreate = false })
				}
			})
		})
		if !open {
			a.gitBranchCreate = false
		}
	}
	if a.gitBranchDelete {
		open := true
		ui.Modal(c, &open, func() {
			ui.Text(c, "删除本地分支").FontSize(14).Bold()
			ui.Text(c, "选择已合并的本地分支。当前分支不在此列表中，服务器分支会保留。").FontSize(11).TextColor(c.Theme().TextMuted).Wrap().MaxWidth(440)
			names := a.deletableGitBranches()
			valid := false
			for _, name := range names {
				if name == a.gitBranchDeleteName {
					valid = true
					break
				}
			}
			if !valid && len(names) > 0 {
				a.gitBranchDeleteName = names[0]
			}
			if len(names) > 0 {
				a.stringSelect(c, &a.gitBranchDeleteName, names, "要删除的分支", false)
			} else {
				ui.Text(c, "当前没有可删除的本地分支。").FontSize(11)
			}
			if a.gitError != "" {
				ui.Text(c, a.gitError).FontSize(10.5).TextColor(c.Theme().Danger).Wrap().MaxWidth(440)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.gitBranchDelete = false
				}
				if ui.Button(c, "删除本地分支").Label("确认删除本地分支").Disabled(a.gitBusy || len(names) == 0).Clicked() {
					dir, name := a.gitFor, a.gitBranchDeleteName
					a.runGitChange(dir, func() error { return core.DeleteGitBranch(dir, name) }, "已删除本地分支 "+name, func() { a.gitBranchDelete = false })
				}
			})
		})
		if !open {
			a.gitBranchDelete = false
		}
	}
}
