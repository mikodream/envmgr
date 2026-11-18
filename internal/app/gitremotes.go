package app

import (
	"fmt"
	"strings"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

// A select keeps its address/ref value separate from its visible alias.
type gitSelectChoice = selectChoice

func containsGitChoice(options []gitSelectChoice, choice gitSelectChoice) bool {
	for _, option := range options {
		if option == choice {
			return true
		}
	}
	return false
}

func (a *App) selectedGitRemote() (core.GitRemote, bool) {
	preferred := a.gitSelectedRemote
	if preferred == "" {
		preferred = a.gitInfo.PushRemote
	}
	if preferred == "" {
		preferred = "origin"
	}
	for _, remote := range a.gitInfo.Remotes {
		if remote.Name == preferred {
			return remote, true
		}
	}
	if len(a.gitInfo.Remotes) > 0 {
		return a.gitInfo.Remotes[0], true
	}
	return core.GitRemote{}, false
}

func (a *App) gitValueSelect(c *ui.Context, selected *gitSelectChoice, options []gitSelectChoice, label string) bool {
	return a.selectControl(c, selected, options, label, a.gitBusy, true)
}

func (a *App) gitRemotePicker(c *ui.Context, p core.Project) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "远程仓库").FontSize(11.5).Bold()
		ui.Spacer(c)
		if ui.Button(c, "添加地址").Disabled(a.gitBusy).Clicked() {
			a.gitRemoteExisting = false
			a.gitRemoteName = ""
			a.gitRemoteURL = ""
			a.gitRemoteOpen = true
		}
	})
	selected, ok := a.selectedGitRemote()
	if !ok {
		ui.Text(c, "还没有远端地址，可添加 GitHub、Gitee、Gitea 等仓库。").FontSize(11).TextColor(t.TextMuted)
		return
	}
	a.gitSelectedRemote = selected.Name
	initial := gitSelectChoice{Name: selected.Name, Value: selected.URL, Detail: selected.URL}
	options := make([]gitSelectChoice, 0, len(a.gitInfo.Remotes))
	for _, remote := range a.gitInfo.Remotes {
		options = append(options, gitSelectChoice{Name: remote.Name, Value: remote.URL, Detail: remote.URL})
	}
	if !containsGitChoice(options, a.gitRemotePick) {
		a.gitRemotePick = initial
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if a.gitValueSelect(c, &a.gitRemotePick, options, "Git 远程仓库") {
			target := a.gitRemotePick
			a.gitSelectedRemote = target.Name
			a.runGitChange(p.Path, func() error { return core.SetGitDefaultRemote(p.Path, target.Name) }, "已切换默认远端为 "+target.Name)
		}
		if a.iconButton(c, icEdit, "编辑远端地址", false).Disabled(a.gitBusy).Clicked() {
			a.gitRemoteExisting = true
			a.gitRemoteName = selected.Name
			a.gitRemoteURL = selected.URL
			a.gitRemoteOpen = true
		}
		if a.iconButton(c, icTrash, "移除远端地址", true).Disabled(a.gitBusy).Clicked() {
			a.gitRemoteRemove = true
			a.gitRemoteRemoveName = selected.Name
		}
	})
	if selected.PushURL != "" && selected.PushURL != selected.URL {
		ui.Text(c, "推送地址 "+selected.PushURL).FontSize(10).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
	}
}

// remoteBinding is the identity library for the selected remote, shown in a modal.
func (a *App) remoteBinding(c *ui.Context, p core.Project) {
	t := c.Theme()
	selected, ok := a.selectedGitRemote()
	if !ok {
		ui.Text(c, "先添加远端地址，再为它保存多套身份。").FontSize(11).TextColor(t.TextMuted)
		return
	}
	remote := selected.PushURL
	if remote == "" {
		remote = selected.URL
	}
	key, ok := core.NormalizeRemote(remote)
	if !ok {
		ui.Text(c, "此地址为本地路径，可直接修改项目提交身份。").FontSize(11).TextColor(t.TextMuted)
		return
	}
	ui.Text(c, selected.Name+" · "+key).FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(480)
	count := 0
	for _, profile := range a.settings.Profiles {
		profile := profile
		matches := false
		for _, level := range core.RemoteKeyLevels(remote) {
			if strings.EqualFold(level, profile.Key) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		count++
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8).Background(t.SurfaceHover).Radius(8).Children(func() {
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, profile.Display()+" · "+profile.Name).FontSize(11).Bold().SingleLine().Ellipsis("…")
				ui.Text(c, profile.Email).FontSize(10).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			if ui.Button(c, "应用").Label("应用身份 " + profile.Display()).Disabled(a.gitBusy).Clicked() {
				a.runGitChange(p.Path, func() error { return a.svc.SelectGitProfile(p.Path, profile.IdentityID()) }, "已切换提交身份为 "+profile.Name)
				a.gitProfilesOpen = false
			}
			if ui.Button(c, "编辑").Label("编辑身份 " + profile.Display()).Clicked() {
				a.gitBindID = profile.IdentityID()
				a.gitBindKey = profile.Key
				a.gitBindLabel = profile.Label
				a.gitBindName = profile.Name
				a.gitBindEmail = profile.Email
				a.gitBindNote = profile.Note
				a.gitBind = true
				a.gitProfilesOpen = false
			}
		})
	}
	if count == 0 {
		ui.Text(c, "此地址还没有保存身份。").FontSize(11).TextColor(t.TextMuted)
	}
	if ui.Button(c, "为此地址新增身份").Clicked() {
		a.gitBindID = ""
		a.gitBindKey = key
		a.gitBindLabel = ""
		a.gitBindName = ""
		a.gitBindEmail = ""
		a.gitBindNote = ""
		a.gitBind = true
		a.gitProfilesOpen = false
	}
}

func (a *App) runGitChange(dir string, change func() error, message string, after ...func()) {
	if a.gitBusy {
		return
	}
	a.gitBusy = true
	a.gitError = ""
	win := a.win
	queue := a.serviceSync
	go func() {
		err := change()
		var info core.GitInfo
		if err == nil {
			a.svc.InvalidateGit(dir)
			info = a.svc.Git(dir)
		}
		finish := func() {
			a.gitBusy = false
			if err != nil {
				if samePath(a.gitFor, dir) {
					a.gitError = err.Error()
					a.gitSelectedRemote = ""
					a.gitRemotePick = gitSelectChoice{}
					a.gitBranchPick = gitSelectChoice{}
				}
				a.pushToast(err.Error())
				return
			}
			if samePath(a.gitFor, dir) {
				a.gitInfo = info
				a.gitSelectedRemote = info.PushRemote
				a.gitReady = true
				a.gitRemotePick = gitSelectChoice{}
				a.gitBranchPick = gitSelectChoice{}
				a.seedGitBuffers()
				a.loadProjectCodeStats(dir, true)
				for _, done := range after {
					done()
				}
			}
			a.pushToast(message)
		}
		if win != nil {
			win.Update(finish)
		} else if queue != nil {
			queue.post(finish)
		} else {
			finish()
		}
	}()
}

func (a *App) gitProfilesDialog(c *ui.Context) {
	if !a.gitProfilesOpen {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Text(c, "远端身份库").FontSize(14).Bold()
		ui.Text(c, "同一地址可以保存多套身份，应用后用于本项目的下一次提交。").FontSize(11).TextColor(c.Theme().TextMuted).Wrap().MaxWidth(480)
		a.remoteBinding(c, core.Project{Path: a.gitFor})
		if ui.Button(c, "关闭").Clicked() {
			a.gitProfilesOpen = false
		}
	})
	if !open {
		a.gitProfilesOpen = false
	}
}

func (a *App) gitRemoteDialog(c *ui.Context) {
	if a.gitRemoteRemove {
		open := true
		ui.Modal(c, &open, func() {
			ui.Text(c, "移除远端地址").FontSize(14).Bold()
			ui.Text(c, "移除 "+a.gitRemoteRemoveName+" 的本地远端配置，项目文件和服务器仓库会保留。").FontSize(11).TextColor(c.Theme().TextMuted).Wrap().MaxWidth(440)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.gitRemoteRemove = false
				}
				if ui.Button(c, "移除地址").Disabled(a.gitBusy).Clicked() {
					dir, name := a.gitFor, a.gitRemoteRemoveName
					a.runGitChange(dir, func() error { return a.svc.RemoveGitRemote(dir, name) }, "已移除远端地址", func() { a.gitRemoteRemove = false; a.gitSelectedRemote = "" })
				}
			})
		})
		if !open {
			a.gitRemoteRemove = false
		}
	}
	if !a.gitRemoteOpen {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Text(c, "Git 远端地址").FontSize(14).Bold()
		ui.Text(c, "名称是下拉选项的别名，地址是实际远程仓库 URL。").FontSize(11).TextColor(c.Theme().TextMuted).Wrap().MaxWidth(440)
		ui.Text(c, "远端别名").FontSize(11)
		if a.gitRemoteExisting {
			ui.Text(c, a.gitRemoteName).FontSize(12).Bold()
		} else {
			ui.TextInput(c, &a.gitRemoteName).Placeholder("github").Label("远端别名")
		}
		ui.Text(c, "仓库地址").FontSize(11)
		ui.TextInput(c, &a.gitRemoteURL).Placeholder("https://github.com/owner/repo.git").Label("仓库地址")
		if a.gitError != "" {
			ui.Text(c, a.gitError).FontSize(11).TextColor(c.Theme().Danger).Wrap().MaxWidth(440)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				a.gitRemoteOpen = false
			}
			if ui.PrimaryButton(c, "保存地址").Disabled(a.gitBusy).Clicked() {
				name, url := strings.TrimSpace(a.gitRemoteName), strings.TrimSpace(a.gitRemoteURL)
				if name == "" || url == "" {
					a.gitError = "别名和地址都要填写。"
					return
				}
				dir := a.gitFor
				a.runGitChange(dir, func() error { return a.svc.SetGitRemote(dir, name, url) }, fmt.Sprintf("已保存远端 %s", name), func() { a.gitRemoteOpen = false })
			}
		})
	})
	if !open {
		a.gitRemoteOpen = false
	}
}
