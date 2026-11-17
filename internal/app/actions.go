package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"envmgr/internal/core"
)

// ---- derived state ----

// recompute refreshes the per-project resolutions the views read. It runs on
// the main thread, never in the middle of a frame.
func (a *App) recompute() {
	res := make(map[string][]core.Resolution, len(a.projects))
	for _, p := range a.projects {
		res[p.ID] = a.svc.Resolve(p)
	}
	a.resolutions = res
	a.report = core.Diagnose(a.svc)
	if len(a.pinBuf) != len(core.Kinds) {
		a.pinBuf = make([]string, len(core.Kinds))
	}
}

// syncAndRecompute is syncNow plus the resolutions.
func (a *App) syncAndRecompute() {
	a.syncNow()
	a.recompute()
}

// missingCount is how many pinned versions are not installed anywhere.
func (a *App) missingCount() int {
	n := 0
	for _, res := range a.resolutions {
		for _, r := range res {
			if r.Missing {
				n++
			}
		}
	}
	return n
}

// managerCount is how many version managers were found.
func (a *App) managerCount() int {
	n := 0
	for _, m := range a.managers {
		if m.Installed {
			n++
		}
	}
	return n
}

// downloadsKind is the SDK shown on the download page.
func (a *App) downloadsKind() core.Kind {
	if a.downloadKind < 0 || a.downloadKind >= len(core.Kinds) {
		return core.Kinds[0]
	}
	return core.Kinds[a.downloadKind]
}

// visibleRemote filters the download list.
func (a *App) visibleRemote(kind core.Kind) []core.Remote {
	out := make([]core.Remote, 0, len(a.remote))
	for _, r := range a.remote {
		if r.Kind != kind {
			continue
		}
		if a.filterLTS && !r.LTS {
			continue
		}
		out = append(out, r)
	}
	return out
}

// isInstalled reports whether a remote version is already here.
func (a *App) isInstalled(r core.Remote) bool {
	_, ok := core.Exact(a.installed, r.Kind, r.Version)
	return ok
}

// ---- background work ----

// remoteJobID names the job that installs one remote version.
func remoteJobID(r core.Remote) string {
	return "install:" + string(r.Kind) + "@" + r.Version
}

// jobFor returns one of the running or finished jobs.
func (a *App) jobFor(id string) *Job {
	for _, j := range a.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// runningJobs lists the jobs still working, newest first.
func (a *App) runningJobs() []*Job {
	var out []*Job
	for i := len(a.jobs) - 1; i >= 0; i-- {
		if a.jobs[i].Running {
			out = append(out, a.jobs[i])
		}
	}
	return out
}

// dropJob forgets a finished job.
func (a *App) dropJob(job *Job) {
	out := a.jobs[:0]
	for _, j := range a.jobs {
		if j != job {
			out = append(out, j)
		}
	}
	a.jobs = out
}

// remoteAction is what the download page should do with the list it has.
type remoteAction int

const (
	// remoteShowList means the list already belongs to the selected kind.
	remoteShowList remoteAction = iota
	// remoteWait means a fetch for this kind is in flight.
	remoteWait
	// remoteShowError means the last fetch for this kind failed.
	remoteShowError
	// remoteUseCache means another tab fetched this kind earlier.
	remoteUseCache
	// remoteFetch means nothing is known about this kind yet.
	remoteFetch
)

// remoteActionFor decides what the download page needs for one kind. Keeping
// the decision separate from the side effects is what makes the tab switch
// testable: the page asks every frame, and the answer never depends on whether
// a click handler happened to fire.
func (a *App) remoteActionFor(kind core.Kind) remoteAction {
	if a.remoteLoading && a.remoteKind == kind {
		return remoteWait
	}
	if a.remoteLoaded && a.remoteKind == kind {
		if a.remoteErr != "" {
			return remoteShowError
		}
		return remoteShowList
	}
	if len(a.remoteCache[kind]) > 0 {
		return remoteUseCache
	}
	return remoteFetch
}

// ensureRemote makes the list on screen belong to the selected kind, whatever
// path got the page here.
func (a *App) ensureRemote(kind core.Kind) {
	switch a.remoteActionFor(kind) {
	case remoteUseCache:
		a.useCachedRemote(kind)
	case remoteFetch:
		a.loadRemote(kind, false)
	}
}

// useCachedRemote swaps in a list fetched earlier for this kind.
func (a *App) useCachedRemote(kind core.Kind) {
	a.remote = a.remoteCache[kind]
	a.remoteKind = kind
	a.remoteLoaded = true
	a.remoteErr = ""
	a.remoteLoading = false
	a.remoteList = ui.ListState{}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// loadRemote fetches the version list of a kind in the background. A list that
// is already here is reused, so switching the tab back and forth is instant.
func (a *App) loadRemote(kind core.Kind, force bool) {
	if !force && len(a.remoteCache[kind]) > 0 {
		a.useCachedRemote(kind)
		return
	}
	if a.remoteLoading && a.remoteKind == kind && !force {
		return
	}
	a.remoteLoading = true
	a.remoteKind = kind
	a.remoteErr = ""
	if a.win != nil {
		a.win.Invalidate()
	}
	go func() {
		list, err := a.svc.ListRemote(context.Background(), kind, force)
		finish := func() {
			a.remoteLoading = false
			a.remoteKind = kind
			a.remoteLoaded = true
			if err != nil {
				a.remoteErr = err.Error()
				a.remote = nil
				return
			}
			a.remoteErr = ""
			a.remote = list
			a.cacheRemote(kind, list)
			a.remoteList = ui.ListState{}
		}
		if a.win == nil {
			finish()
			return
		}
		a.win.Update(finish)
	}()
}

// cacheRemote remembers one SDK's list for the next visit to its tab.
func (a *App) cacheRemote(kind core.Kind, list []core.Remote) {
	if len(list) == 0 {
		return
	}
	if a.remoteCache == nil {
		a.remoteCache = map[core.Kind][]core.Remote{}
	}
	a.remoteCache[kind] = list
}

// startInstall downloads and unpacks one version in the background.
func (a *App) startInstall(r core.Remote) {
	job := &Job{ID: remoteJobID(r), Label: "安装 " + core.Info(r.Kind).Name + " " + r.Version, Running: true}
	a.jobs = append(a.jobs, job)
	ctx, cancel := context.WithCancel(context.Background())
	a.cancels = append(a.cancels, cancel)
	win := a.win

	notify := func(fn func()) {
		if win == nil {
			fn()
			return
		}
		win.Update(fn)
	}

	go func() {
		inst, err := a.svc.Install(ctx, r, func(done, total int64) {
			notify(func() {
				job.Done, job.Total = done, total
				job.Detail = describeProgress(done, total)
			})
		})
		notify(func() {
			job.Running = false
			a.dropJob(job)
			if err != nil {
				a.pushToast("安装失败：" + err.Error())
				return
			}
			a.syncAndRecompute()
			a.pushToast("已安装 " + core.Info(inst.Kind).Name + " " + inst.Version)
			a.remoteList = ui.ListState{}
		})
	}()
}

// installManager fetches a version manager from its official release.
func (a *App) installManager(m core.ManagerState) {
	job := &Job{ID: "manager:" + m.ID, Label: "安装 " + m.Name, Running: true}
	a.jobs = append(a.jobs, job)
	win := a.win
	notify := func(fn func()) {
		if win == nil {
			fn()
			return
		}
		win.Update(fn)
	}
	go func() {
		_, err := a.svc.InstallManager(context.Background(), m.ID, func(done, total int64) {
			notify(func() {
				job.Done, job.Total = done, total
				job.Detail = describeProgress(done, total)
			})
		})
		notify(func() {
			job.Running = false
			a.dropJob(job)
			if err != nil {
				a.pushToast(m.Name + " 安装失败：" + err.Error())
				return
			}
			a.syncAndRecompute()
			a.pushToast(m.Name + " 已就绪")
		})
	}()
}

// describeProgress is the line under a progress bar.
func describeProgress(done, total int64) string {
	if total <= 0 {
		return core.HumanSize(done)
	}
	return fmt.Sprintf("%s / %s", core.HumanSize(done), core.HumanSize(total))
}

// pushToast queues a message to show on the next frame. Background work can
// not call c.Toast itself: there is no frame there.
func (a *App) pushToast(msg string) {
	a.pendingToasts = append(a.pendingToasts, msg)
	a.flushToasts()
}

// flushToasts shows any queued toast once a frame is running.
func (a *App) flushToasts() {
	// The view drains this list; nothing to do without one.
}

// AddProject is the menu action behind 文件 ▸ 添加项目….
func (a *App) AddProject() { a.addProject() }

// Rescan is the menu action behind 文件 ▸ 重新扫描.
func (a *App) Rescan() { a.refreshAsync() }

// ---- project actions ----

// addProject asks for a directory and adds it to the list.
func (a *App) addProject() {
	win := a.win
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       "选择项目目录",
			ButtonLabel: "添加",
			Directory:   true,
			Multiple:    false,
		})
		if err != nil || len(dirs) == 0 {
			return
		}
		p, err := a.svc.AddProject(dirs[0])
		refresh := func() {
			a.syncAndRecompute()
			if err != nil {
				a.pushToast("添加失败：" + err.Error())
				return
			}
			a.page = PageProjects
			for i, row := range a.projectRows() {
				if row.ID == p.ID {
					a.selProject = i
				}
			}
			a.pushToast("已添加项目 " + p.Name)
		}
		if win == nil {
			refresh()
			return
		}
		win.Update(refresh)
	}()
}

// setPin pins a version in the project's .envmgr.json.
func (a *App) setPin(p core.Project, kind core.Kind, version string) {
	err := a.svc.SetPin(p.ID, kind, version)
	if err != nil {
		a.pushToast(err.Error())
		return
	}
	a.syncAndRecompute()
	if version == "" {
		a.pushToast(core.Info(kind).Name + " 已改为跟随项目文件")
	} else {
		a.pushToast(core.Info(kind).Name + " 固定为 " + version)
	}
}

// removeProject forgets a project.
func (a *App) removeProject(p core.Project) {
	if err := a.svc.RemoveProject(p.ID); err != nil {
		a.pushToast(err.Error())
		return
	}
	delete(a.resolutions, p.ID)
	a.syncAndRecompute()
	if a.selProject >= len(a.projectRows()) {
		a.selProject = len(a.projectRows()) - 1
	}
	a.pushToast("已移除 " + p.Name)
}

// openTerminal starts a terminal in the project with its env applied.
func (a *App) openTerminal(p core.Project) {
	terms := core.Terminals()
	if len(terms) == 0 {
		a.pushToast("没有找到可用的终端程序")
		return
	}
	env := a.svc.EnvFor(p)
	go func() {
		if err := core.LaunchTerminal(p.Path, env, terms[0]); err != nil {
			a.pushToast("打开终端失败：" + err.Error())
		}
	}()
	a.svc.Touch(p.ID)
}

// ---- settings ----

// saveSettings writes the settings page's fields to the store.
func (a *App) saveSettings() {
	cfg := a.svc.Config()
	cfg.DataDir = a.dataDir
	cfg.NodeMirror = a.nodeMirror
	cfg.Theme = a.themeMode
	cfg.FollowMarkers = a.followMark
	cfg.ShellHook = a.shellHook
	if err := a.svc.SetConfig(cfg); err != nil {
		a.pushToast("保存设置失败：" + err.Error())
		return
	}
	a.syncAndRecompute()
}

// defaultDataDirHint is the placeholder of the data directory field.
func defaultDataDirHint() string {
	if dir, err := core.ConfigDir(); err == nil {
		return filepath.Join(dir, "sdk")
	}
	return "~/.envmgr/sdk"
}

// ---- desktop helpers ----

// openPath reveals a file or folder in the system file manager.
func openPath(p string) {
	go func() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			mygo.Shell.ShowItemInFolder(p)
			return
		}
		_ = mygo.Shell.OpenPath(p)
	}()
}

// openURL opens a link in the default browser.
func openURL(u string) {
	go func() { _ = mygo.Shell.OpenExternal(u) }()
}

// mygoRevealPath shows a file, or its folder when it does not exist yet.
func mygoRevealPath(p string) {
	go func() {
		if _, err := os.Stat(p); err != nil {
			mygo.Shell.ShowItemInFolder(filepath.Dir(p))
			return
		}
		mygo.Shell.ShowItemInFolder(p)
	}()
}

// nowLabel is the "updated at" time in the sidebar.
func nowLabel() string {
	return time.Now().Format("15:04:05")
}

// ---- overlays ----

// OpenAddDialog opens the manual-add dialog from outside a frame, so the
// snapshot tool can render it.
func (a *App) OpenAddDialog() { a.openAddVersion() }

// openAddVersion starts the "add an installed version by hand" dialog.
func (a *App) openAddVersion() {
	a.addKindIdx = a.toolchainKind
	a.addPath = ""
	a.addVer = ""
	a.addNote = ""
	a.addError = ""
	a.addOpen = true
}

// pickAddPath opens the native directory picker.
func (a *App) pickAddPath() {
	kind := core.Kinds[a.addKindIdx]
	go func() {
		dir, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    a.win,
			Title:     "选择已安装的 " + core.Info(kind).Name + " 目录",
			Directory: true,
		})
		if err != nil || len(dir) == 0 {
			return
		}
		picked := dir[0]
		version := core.ProbeVersion(kind, picked)
		if a.win != nil {
			a.win.Update(func() {
				a.addPath = picked
				if version != "" {
					a.addVer = version
				}
				a.addError = ""
			})
		}
	}()
}

// saveAddVersion records the hand-added version.
func (a *App) saveAddVersion() {
	kind, path, version := core.Kinds[a.addKindIdx], a.addPath, strings.TrimSpace(a.addVer)
	if path == "" {
		a.addError = "先选一个目录"
		return
	}
	go func() {
		inst, err := a.svc.AddManualInstall(kind, version, path, a.addNote)
		a.win.Update(func() {
			if err != nil {
				a.addError = err.Error()
				return
			}
			a.addOpen = false
			a.toolchainKind = kindIndexOf(kind)
			a.pushToast("已添加 " + core.Info(inst.Kind).Name + " " + inst.Version)
			a.syncAndRecompute()
		})
	}()
}

// dialogs draws the confirmations and drains queued toasts.
func (a *App) dialogs(c *ui.Context) {
	for _, msg := range a.pendingToasts {
		c.Toast(msg)
	}
	a.pendingToasts = nil

	a.vendorPicker(c)
	a.gitBindDialog(c)

	if inst := a.confirmRemove; inst != nil {
		open := true
		info := core.Info(inst.Kind)
		ui.Modal(c, &open, func() {
			ui.Text(c, "删除 "+info.Name+" "+inst.Version+"？").FontSize(14).Bold()
			ui.Text(c, "这会把 "+inst.Path+" 从磁盘上移除，无法撤销。").FontSize(11).
				TextColor(c.Theme().TextMuted).Wrap().MaxWidth(380)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.confirmRemove = nil
				}
				if ui.PrimaryButton(c, "删除").Clicked() {
					target := *inst
					a.confirmRemove = nil
					go func() {
						if err := a.svc.Remove(target); err != nil {
							a.pushToast(err.Error())
						} else {
							a.pushToast("已删除 " + target.ID())
						}
						if a.win != nil {
							a.win.Update(func() { a.syncAndRecompute() })
						}
					}()
				}
			})
		})
		if !open {
			a.confirmRemove = nil
		}
	}

	if p := a.confirmDelete; p != nil {
		open := true
		ui.Modal(c, &open, func() {
			ui.Text(c, "移除项目 "+p.Name+"？").FontSize(14).Bold()
			ui.Text(c, "只从 EnvMgr 的列表里移除，磁盘上的文件不受影响。").FontSize(11).
				TextColor(c.Theme().TextMuted).Wrap().MaxWidth(380)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.confirmDelete = nil
				}
				if ui.PrimaryButton(c, "移除").Clicked() {
					target := *p
					a.confirmDelete = nil
					a.removeProject(target)
				}
			})
		})
		if !open {
			a.confirmDelete = nil
		}
	}

	if a.addOpen {
		a.addVersionDialog(c)
	}

	if inst := a.confirmForget; inst != nil {
		open := true
		info := core.Info(inst.Kind)
		ui.Modal(c, &open, func() {
			ui.Text(c, "不再列出 "+info.Name+" "+inst.Version+"？").FontSize(14).Bold()
			ui.Text(c, "只是从 EnvMgr 的列表里去掉，"+inst.Path+" 保持原样。").
				FontSize(11).TextColor(c.Theme().TextMuted).Wrap().MaxWidth(400)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.confirmForget = nil
				}
				if ui.PrimaryButton(c, "移除").Clicked() {
					target := *inst
					a.confirmForget = nil
					go func() {
						err := a.svc.RemoveManualInstall(target.Kind, target.Path)
						if a.win != nil {
							a.win.Update(func() {
								if err != nil {
									a.pushToast(err.Error())
								} else {
									a.pushToast("已从列表移除 " + target.ID())
								}
								a.syncAndRecompute()
							})
						}
					}()
				}
			})
		})
		if !open {
			a.confirmForget = nil
		}
	}
}

// addVersionDialog lets the user point EnvMgr at an SDK that lives somewhere
// discovery does not look.
func (a *App) addVersionDialog(c *ui.Context) {
	t := c.Theme()
	kind := core.Kinds[a.addKindIdx]
	open := true
	ui.Modal(c, &open, func() {
		ui.Text(c, "添加已安装的版本").FontSize(14).Bold()
		ui.Text(c, "选一个 SDK 目录，EnvMgr 会读取它的版本号，之后就能像其他版本一样给项目固定。").
			FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(440)

		a.sectionLabel(c, "SDK")
		ui.Segmented(c, &a.addKindIdx, kindNames()...).Label("SDK 类型").Changed()

		a.sectionLabel(c, "目录")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &a.addPath).Label("SDK 目录").
				Placeholder(`例如 C:\Users\你\env\java\17`).Grow(1).MinWidth(0)
			if ui.Button(c, "选择…").Clicked() {
				a.pickAddPath()
			}
		})

		a.sectionLabel(c, "版本")
		ui.TextInput(c, &a.addVer).Label("版本号").
			Placeholder("留空则从目录自动识别，例如 " + versionExample(kind))

		if a.addError != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icAlert).Size(13, 13).TextColor(t.Danger)
				ui.Text(c, a.addError).FontSize(11).TextColor(t.Danger).Wrap().MaxWidth(400)
			})
		}

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				a.addOpen = false
			}
			if ui.PrimaryButton(c, "添加").Clicked() {
				a.saveAddVersion()
			}
		})
	})
	if !open {
		a.addOpen = false
	}
}

// jobsBar is the small card in the corner listing running work.
func (a *App) jobsBar(c *ui.Context) {
	jobs := a.runningJobs()
	if len(jobs) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c).Absolute().Right(18).Bottom(18).Width(300).ZIndex(50).
		Padding(12).Radius(11).Background(t.Surface).Border(1, t.Border).
		Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.18)).Gap(8).Children(func() {
		for _, j := range jobs {
			ui.Column(c).Gap(5).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(11, 11)
					ui.Text(c, j.Label).FontSize(11.5).Bold().Grow(1).SingleLine().Ellipsis("…")
					ui.Text(c, j.Detail).FontSize(10).TextColor(t.TextMuted).SingleLine()
				})
				ui.Progress(c, j.Progress()).Label(j.Label)
			})
		}
	})
}
