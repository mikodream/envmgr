package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"envmgr/internal/core"
)

// gitAddressLabel is what to print for a repository's commit address: the name of
// the binding that covers it, or the domain when no binding does.
//
// The second result reports whether a binding matched, so callers can colour a
// bound address differently from a plain one.
func (a *App) gitAddressLabel(remote string) (string, bool) {
	if match := core.MatchGitProfile(remote, a.settings.Profiles); match != nil {
		return match.Display(), true
	}
	if key, ok := core.NormalizeRemote(remote); ok {
		return core.Domain(key), false
	}
	return remote, false
}

// gitPanel is the Git card on a project's page. Git is the one tool every
// project has an opinion about but nobody configures per project, so the card
// answers two questions at a glance: is this a repository at all, and whose name
// and email will the next commit carry.
func (a *App) gitPanel(c *ui.Context, p core.Project) {
	t := c.Theme()
	a.syncGit(p)

	card := a.card(c).Padding(14).Gap(10)
	card.Children(func() {
		header := ui.Row(c).Gap(10).AlignItems(ui.Center)
		header.Children(func() {
			ui.Box(c).Size(26, 26).Radius(7).Background(t.SurfaceHover).Center().Children(func() {
				ui.Icon(c, icBranch).Size(15, 15).TextColor(t.TextMuted)
			})
			ui.Text(c, "Git").FontSize(12.5).Bold()
			if a.gitReady {
				a.gitStateChip(c)
			} else {
				a.chip(c, "读取中", t.TextMuted, t.SurfaceHover)
			}
			ui.Spacer(c)
			if a.gitInfo.Available && a.gitInfo.Repo {
				if a.button(c, icRefresh, "刷新").Label("刷新 Git 状态").Clicked() {
					// Refresh in the background: a large repository can take a
					// while to report its status.
					a.svc.RefreshGit(p.Path)
				}
			}
		})

		switch {
		case !a.gitReady:
			ui.Text(c, "正在后台读取 Git 状态…").FontSize(11).TextColor(t.TextMuted)
		case !a.gitInfo.Available:
			a.gitMissing(c)
		case !a.gitInfo.Repo:
			a.gitNotRepo(c, p)
			ui.Box(c).Height(1).FillWidth().Background(t.Border)
			a.gitCodeStats(c, p)
		default:
			a.gitRepoBody(c, p)
		}

		if a.gitError != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icAlert).Size(12, 12).TextColor(t.Danger)
				ui.Text(c, a.gitError).FontSize(10.5).TextColor(t.Danger).
					Wrap().MaxWidth(620)
			})
		}
	})
}

// syncGit reads only the cache while drawing. A first read is queued in the
// background, so even a slow repository never blocks a wheel animation frame.
//
// It deliberately does not close the editor: the editor is opened by the user,
// and the first frame after opening a project sees a changed path, so resetting
// here would close what was just opened.
func (a *App) syncGit(p core.Project) {
	first := a.gitFor != p.Path
	previous := a.gitInfo
	a.gitFor = p.Path
	a.gitInfo, a.gitReady = a.svc.PeekGit(p.Path)
	if first || (a.gitReady && previous.PushRemote != a.gitInfo.PushRemote) {
		a.gitSelectedRemote = ""
		a.gitRemotePick = gitSelectChoice{}
	}
	if first || previous.Branch != a.gitInfo.Branch || previous.Detached != a.gitInfo.Detached {
		a.gitBranchPick = gitSelectChoice{}
	}
	if first {
		a.gitBranchCreate = false
		a.gitBranchDelete = false
		a.gitProfilesOpen = false
		a.gitRemoteOpen = false
		a.gitRemoteRemove = false
		a.gitError = ""
		if a.codeStatsFor != p.Path {
			if a.codeStatsCancel != nil {
				a.codeStatsCancel()
			}
			a.codeStatsGeneration++
			a.codeStatsFor = p.Path
			a.codeStats = core.CodeStats{}
			a.codeStatsReady = false
			a.codeStatsLoading = false
		}
	}
	if a.gitReady && (first || !a.gitEdit) {
		a.seedGitBuffers()
	}
}

// seedGitBuffers fills the editor with what a commit would use right now, so
// opening the editor shows the current values rather than empty boxes.
func (a *App) seedGitBuffers() {
	a.gitName = a.gitInfo.Identity.Name
	a.gitEmail = a.gitInfo.Identity.Email
}

// tone is which theme color a chip uses.
type tone int

const (
	toneMuted tone = iota
	toneGood
	toneWarn
	toneAccent
)

// gitState words the repository state, and is a pure function so the verdict
// can be tested without drawing.
func gitState(g core.GitInfo) (label string, t tone) {
	switch {
	case !g.Available:
		return "未安装", toneMuted
	case !g.Repo:
		return "不是仓库", toneWarn
	case g.Detached:
		return "游离 HEAD", toneWarn
	case !g.Commits:
		return "还没有提交", toneMuted
	case g.Clean():
		return "干净", toneGood
	default:
		return fmt.Sprintf("%d 项改动", g.Changed()), toneAccent
	}
}

// gitStateChip is the one-word verdict next to the title.
func (a *App) gitStateChip(c *ui.Context) {
	t := c.Theme()
	label, tn := gitState(a.gitInfo)
	switch tn {
	case toneGood:
		a.chip(c, label, t.Success, t.Success.Alpha(0.12))
	case toneWarn:
		a.chip(c, label, t.Warning, t.Warning.Alpha(0.12))
	case toneAccent:
		a.chip(c, label, t.Accent, t.Accent.Alpha(0.10))
	default:
		a.chip(c, label, t.TextMuted, t.SurfaceHover)
	}
}

// gitMissing is the case where the git program itself is absent: nothing about
// the project can be read, and initialising would not help.
func (a *App) gitMissing(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "这台机器上没有找到 git，所以无法读取或初始化仓库。").
		FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(620)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "了解 Git").Label("打开 git-scm.com 下载").Clicked() {
			openPath("https://git-scm.com/downloads")
		}
	})
}

// gitNotRepo offers to initialise. The identity that would be used is shown
// first, because initialising a repository with no identity set makes the first
// commit fail later, which is a confusing place to discover it.
func (a *App) gitNotRepo(c *ui.Context, p core.Project) {
	t := c.Theme()
	ui.Text(c, "这个目录还不是 Git 仓库。初始化后会创建 .git，不会改动项目里的任何文件。").
		FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(620)

	a.identityRows(c, p)

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if a.gitBusy {
			ui.Text(c, "正在初始化…").FontSize(11).TextColor(t.TextMuted)
			return
		}
		if ui.PrimaryButton(c, "初始化 Git 仓库").Clicked() {
			a.initGit(p)
		}
		if !a.gitInfo.Identity.Complete() {
			ui.Text(c, "初始化前建议先填好用户名和邮箱，否则第一次提交会失败。").
				FontSize(10.5).TextColor(t.Warning).SingleLine().Ellipsis("…")
		}
	})
}

// gitRepoBody shows the repository state and the identity in use.
func (a *App) gitRepoBody(c *ui.Context, p core.Project) {
	t := c.Theme()
	a.gitRemotePicker(c, p)
	ui.Box(c).Height(1).FillWidth().Background(t.Border)
	a.gitBranchPanel(c, p)
	ui.Box(c).Height(1).FillWidth().Background(t.Border)
	a.identityRows(c, p)
	ui.Box(c).Height(1).FillWidth().Background(t.Border)
	a.gitCodeStats(c, p)
}

// remoteBinding shows the identity that the remote address selects, and the way
// to bind one. The address is the key because it is what differs between
// identities: the same person commits to a company org as one name and to their
// own repositories as another.
// gitBindDialog is the remote-address binding editor, shown as a dialog so the
// form is never clipped by the project page's scroll area.
func (a *App) gitBindDialog(c *ui.Context) {
	if !a.gitBind {
		return
	}
	t := c.Theme()
	open := true
	ui.Modal(c, &open, func() {
		ui.Text(c, "按远端地址绑定提交身份").FontSize(14).Bold()
		ui.Text(c, "同一个地址可以保存多套身份。保存后回到地址列表，点击「应用此身份」切换下一次提交的署名。").
			FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(440)
		ui.Column(c).Gap(8).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				ui.TextInput(c, &a.gitBindKey).Label("远端地址").Placeholder("github.com/acme")
				ui.Text(c, "现在这个仓库是 "+a.gitInfo.Remote).FontSize(10).
					TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			ui.Column(c).Gap(3).Children(func() {
				ui.TextInput(c, &a.gitBindLabel).Label("名字（可留空）").Placeholder("公司账号")
				ui.Text(c, "起名字后项目里显示这个名字，留空就显示域名。").
					FontSize(10).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &a.gitBindName).Label("用户名 user.name")
				ui.TextInput(c, &a.gitBindEmail).Label("邮箱 user.email")
			})
		})
		if a.gitError != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icAlert).Size(12, 12).TextColor(t.Danger)
				ui.Text(c, a.gitError).FontSize(10.5).TextColor(t.Danger).Wrap().MaxWidth(440)
			})
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if a.gitBusy {
				ui.Text(c, "正在保存…").FontSize(11).TextColor(t.TextMuted)
				return
			}
			if ui.Button(c, "取消").Clicked() {
				a.gitBind = false
				a.gitError = ""
				return
			}
			if ui.PrimaryButton(c, "保存绑定").Clicked() {
				a.saveBinding("")
			}
		})
	})
	if !open {
		a.gitBind = false
		a.gitError = ""
	}
}

// saveBinding stores the binding being edited.
func (a *App) saveBinding(currentKey string) {
	profile := core.GitProfile{
		ID:    a.gitBindID,
		Key:   strings.TrimSpace(a.gitBindKey),
		Label: strings.TrimSpace(a.gitBindLabel),
		Name:  strings.TrimSpace(a.gitBindName),
		Email: strings.TrimSpace(a.gitBindEmail),
		Note:  strings.TrimSpace(a.gitBindNote),
	}
	if profile.Key == "" || profile.Name == "" || profile.Email == "" {
		a.gitError = "地址、用户名、邮箱都要填。"
		return
	}
	if normalized, ok := core.NormalizeRemote(profile.Key); ok {
		profile.Key = normalized
	} else {
		a.gitError = "地址看起来不完整，例如 github.com/acme 或 git@github.com:acme/app.git。"
		return
	}
	a.gitBusy = true
	a.gitError = ""
	dir := a.gitFor
	if a.win != nil {
		a.win.Invalidate()
	}
	go func() {
		err := a.svc.SaveGitProfile(profile)
		finish := func() {
			a.gitBusy = false
			if err != nil {
				a.gitError = err.Error()
				a.pushToast("保存失败: " + err.Error())
				return
			}
			a.gitBind = false
			if dir != "" {
				a.gitInfo = a.svc.Git(dir)
				a.seedGitBuffers()
			}
			a.pushToast("已绑定 " + profile.Key + " → " + profile.Name)
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}()
	_ = currentKey
}

// applyProfileLocally writes a binding into this repository's own config, which
// works even when the global rules are not installed.
func (a *App) applyProfileLocally(p core.Project, profile core.GitProfile) {
	if a.gitBusy {
		return
	}
	a.gitBusy = true
	a.gitError = ""
	dir := p.Path
	if a.win != nil {
		a.win.Invalidate()
	}
	go func() {
		err := a.svc.ApplyGitProfileTo(dir, profile)
		finish := func() {
			a.gitBusy = false
			if err != nil {
				a.gitError = err.Error()
				a.pushToast("写入失败: " + err.Error())
				return
			}
			a.gitInfo = a.svc.Git(dir)
			a.seedGitBuffers()
			a.gitEdit = false
			a.pushToast("已把 " + profile.Name + " 写进本仓库")
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}()
}

// deleteGitProfile removes one remote-address binding and its rule.
func (a *App) deleteGitProfile(key string) {
	if a.gitBusy {
		return
	}
	a.gitBusy = true
	if a.win != nil {
		a.win.Invalidate()
	}
	go func() {
		err := a.svc.DeleteGitProfile(key)
		finish := func() {
			a.gitBusy = false
			if err != nil {
				a.gitError = err.Error()
				a.pushToast("删除失败: " + err.Error())
				return
			}
			a.pushToast("已删除绑定 " + key)
			if a.gitFor != "" {
				a.gitInfo = a.svc.Git(a.gitFor)
				a.seedGitBuffers()
			}
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}()
}

// toggleGitRules installs or removes the global include rules.
func (a *App) toggleGitRules(on bool) {
	if a.gitBusy {
		return
	}
	a.gitBusy = true
	a.gitError = ""
	if a.win != nil {
		a.win.Invalidate()
	}
	go func() {
		var err error
		if on {
			err = a.svc.InstallGitRules()
		} else {
			err = a.svc.UninstallGitRules()
		}
		finish := func() {
			a.gitBusy = false
			if err != nil {
				a.gitError = err.Error()
				a.pushToast("规则操作失败: " + err.Error())
				return
			}
			a.gitInfo = a.svc.Git(a.gitFor)
			if on {
				a.pushToast("已安装自动套用规则")
			} else {
				a.pushToast("已停止自动套用，全局配置里 EnvMgr 的规则已移除")
			}
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}()
}

// gitFact is one label/value pair in the status row.
func (a *App) gitFact(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(10.5).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(11.5).Font("monospace").SingleLine().Ellipsis("…")
	})
}

// changeSummary words the status counts.
func changeSummary(g core.GitInfo) string {
	if g.Clean() {
		return "没有未提交的改动"
	}
	var parts []string
	if g.Staged > 0 {
		parts = append(parts, fmt.Sprintf("已暂存 %d", g.Staged))
	}
	if g.Modified > 0 {
		parts = append(parts, fmt.Sprintf("已修改 %d", g.Modified))
	}
	if g.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("未跟踪 %d", g.Untracked))
	}
	return strings.Join(parts, " · ")
}

// identityRows shows user.name and user.email with where each value comes from,
// and the editor to write a project-level override.
func (a *App) identityRows(c *ui.Context, p core.Project) {
	t := c.Theme()
	id := a.gitInfo.Identity

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "提交身份").FontSize(11).Bold().TextColor(t.TextMuted)
		ui.Text(c, "user.name / user.email").FontSize(10).
			Font("monospace").TextColor(t.TextMuted)
		ui.Spacer(c)
		if a.gitInfo.Repo && len(a.gitInfo.Remotes) > 0 && !a.gitEdit {
			if ui.Button(c, "身份库").Label("打开远端身份库").Clicked() {
				a.gitProfilesOpen = true
			}
		}
		if !a.gitEdit {
			// "设置身份" when there is nothing to inherit; otherwise the action
			// is a change, not a prerequisite.
			label := "修改"
			if !id.Complete() {
				label = "设置身份"
			}
			if a.button(c, icEdit, label).Clicked() {
				a.gitEdit = true
				a.seedGitBuffers()
			}
		}
	})

	if a.gitEdit {
		a.gitIdentityEditor(c, p)
		return
	}

	ui.Grid(c).Columns(2).Gap(10).Children(func() {
		a.identityValue(c, "用户名", id.Name, id.NameSource)
		a.identityValue(c, "邮箱", id.Email, id.EmailSource)
	})

	if id.NameSource == core.SourceRule || id.EmailSource == core.SourceRule {
		ui.Text(c, "标着「远端绑定」的项是按这个仓库的远端地址选出来的，文件在「~/.envmgr/git/」下，由全局 git 配置里的条件包含引入。").
			FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(620)
	} else if id.NameSource == core.SourceGlobal || id.EmailSource == core.SourceGlobal {
		ui.Text(c, "标着「全局」的项沿用了 ~/.gitconfig；在这里保存会写入本项目自己的 .git/config，只影响这个仓库。").
			FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(620)
	}
	if !id.Complete() && a.gitInfo.Available {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icAlert).Size(12, 12).TextColor(t.Warning)
			ui.Text(c, "用户名或邮箱还没有设置，git commit 会拒绝执行。").
				FontSize(10.5).TextColor(t.Warning)
		})
	}
}

// identityValue is one setting: the value, and a chip naming its origin.
func (a *App) identityValue(c *ui.Context, label, value string, src core.Source) {
	t := c.Theme()
	box := ui.Column(c).Gap(3).Padding(9, 10).Radius(8).Background(t.SurfaceHover)
	box.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(10.5).TextColor(t.TextMuted)
			ui.Spacer(c)
			switch src {
			case core.SourceProject:
				a.chip(c, "本项目", t.Accent, t.Accent.Alpha(0.12))
			case core.SourceRule:
				// The value was chosen because the remote address matched a
				// binding, which is different from inheriting the global one.
				a.chip(c, "远端绑定", t.Success, t.Success.Alpha(0.12))
			case core.SourceGlobal:
				a.chip(c, "全局", t.TextMuted, t.Background)
			default:
				a.chip(c, "未设置", t.Warning, t.Warning.Alpha(0.12))
			}
		})
		text := value
		if text == "" {
			text = "—"
		}
		ui.Text(c, text).FontSize(12).SingleLine().Ellipsis("…")
	})
}

// gitIdentityEditor writes the project-level override. Leaving a field empty
// removes the override, which is how the global value comes back.
func (a *App) gitIdentityEditor(c *ui.Context, p core.Project) {
	t := c.Theme()
	id := a.gitInfo.Identity

	ui.Grid(c).Columns(2).Gap(10).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.TextInput(c, &a.gitName).Label("用户名 user.name").
				Placeholder(id.GlobalName)
		})
		ui.Column(c).Gap(3).Children(func() {
			ui.TextInput(c, &a.gitEmail).Label("邮箱 user.email").
				Placeholder(id.GlobalEmail)
		})
	})

	hint := "写入本项目 .git/config；留空则删除本项目设置，回到全局的值。"
	if !a.gitInfo.Repo {
		hint = "这个目录还不是仓库，身份会在初始化时一并写入。"
	}
	ui.Text(c, hint).FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(620)

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if a.gitBusy {
			ui.Text(c, "正在保存…").FontSize(11).TextColor(t.TextMuted)
			return
		}
		if ui.PrimaryButton(c, "保存").Clicked() {
			a.saveGitIdentity(p)
		}
		if ui.Button(c, "取消").Clicked() {
			a.gitEdit = false
			a.gitError = ""
			a.seedGitBuffers()
		}
		if id.GlobalName != "" || id.GlobalEmail != "" {
			ui.Spacer(c)
			if ui.Button(c, "沿用全局").Label("清空，改用全局身份").Clicked() {
				a.gitName, a.gitEmail = "", ""
				a.saveGitIdentity(p)
			}
		}
	})
}

// initGit runs git init off the draw loop and reloads the panel afterwards.
func (a *App) initGit(p core.Project) {
	if a.gitBusy {
		return
	}
	a.gitBusy = true
	a.gitError = ""
	if a.win != nil {
		a.win.Invalidate()
	}
	go func(path string) {
		info, err := a.svc.InitGit(path)
		finish := func() {
			a.gitBusy = false
			if err != nil {
				a.gitError = err.Error()
				a.pushToast("初始化失败: " + err.Error())
				return
			}
			a.gitFor = path
			a.gitInfo = info
			a.seedGitBuffers()
			a.pushToast("已初始化 Git 仓库")
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}(p.Path)
}

// saveGitIdentity writes user.name and user.email for this project.
func (a *App) saveGitIdentity(p core.Project) {
	if a.gitBusy {
		return
	}
	if !a.gitInfo.Repo {
		a.gitError = "这个目录还不是仓库，请先初始化 Git。"
		return
	}
	a.gitBusy = true
	a.gitError = ""
	name, email := a.gitName, a.gitEmail
	if a.win != nil {
		a.win.Invalidate()
	}
	go func(path string) {
		err := a.svc.SetGitIdentity(path, name, email)
		finish := func() {
			a.gitBusy = false
			if err != nil {
				a.gitError = err.Error()
				a.pushToast("保存失败: " + err.Error())
				return
			}
			a.gitInfo = a.svc.Git(path)
			a.gitEdit = false
			a.seedGitBuffers()
			a.pushToast("已保存本项目的 Git 身份")
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}(p.Path)
}

// samePath compares directories the way the platform would.
func samePath(a, b string) bool {
	norm := func(p string) string {
		p = strings.ReplaceAll(p, "\\", "/")
		p = strings.TrimRight(p, "/")
		return strings.ToLower(p)
	}
	return norm(a) == norm(b)
}
