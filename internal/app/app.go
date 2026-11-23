package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"envmgr/internal/core"
)

// Pages of the window, in sidebar order.
const (
	PageDashboard = iota
	PageProjects
	PageToolchains
	PageDownloads
	PageDoctor
	PageSettings
)

var pageTitles = []struct {
	Title string
	Desc  string
	Icon  *ui.SVG
}{
	{"概览", "项目环境、已装 SDK 与需要处理的问题", icGauge},
	{"项目", "为每个项目固定 SDK 版本，进入目录即切换", icFolder},
	{"工具链", "已装 SDK 与版本管理器，分别查看和管理", icLayers},
	{"下载安装", "从官方与镜像源安装新的 SDK 版本", icDownload},
	{"体检", "逐项检查配置，包括终端钩子是否装好", icCheck},
	{"设置", "数据目录、镜像、终端与外观", icSettings},
}

// Job is one background operation with progress the UI shows.
type Job struct {
	ID         string
	Label      string
	Stage      string
	Detail     string
	Done       int64
	Total      int64
	Err        error
	Running    bool
	Cancelling bool
	Status     JobStatus

	cancel       context.CancelFunc
	run          jobWork
	initialStage string
	workerDone   chan struct{}
}

// Progress reports 0..1, or -1 when the size is unknown.
func (j Job) Progress() float64 {
	if j.Total <= 0 {
		return -1
	}
	return float64(j.Done) / float64(j.Total)
}

// App is the window's state: the service, what the last refresh found, and
// the controls' current values.
//
// Every field below is read and written on the main thread only: background
// work hands results over through win.Update.
type App struct {
	svc *core.Service
	win *mygo.Window

	page int

	// Discovery snapshot.
	installed []core.Installed
	inventory inventoryView
	settings  settingsSnapshot
	// defaults is the global fallback per SDK, read with the snapshot so the
	// settings page never touches the store while drawing.
	defaults map[core.Kind]string
	doctor   doctorView
	projects []core.Project
	managers []core.ManagerState
	syncedAt string
	syncing  bool

	// Projects page.
	projectQuery string
	selProject   int
	projectList  ui.ListState
	// projectOpen switches the page from the card wall to one project's
	// configuration, where the list shrinks to the left column.
	projectOpen bool
	// expandEmpty reveals the SDKs this project does not configure.
	expandEmpty bool

	// Git panel of the opened project.
	gitReady bool
	gitInfo  core.GitInfo
	// gitFor is the directory gitInfo describes, so a selection change refetches
	// instead of showing another project's repository.
	gitFor string
	// gitName and gitEmail are the edit buffers, seeded from whatever the
	// repository or the global config currently provides.
	gitName  string
	gitEmail string
	// gitEdit opens the identity editor.
	gitEdit bool
	// gitBusy is set while an init or a write is in flight.
	gitBusy bool
	// gitError shows why an init or write failed.
	gitError string
	// Bindings of an identity to a remote address. gitBind opens the editor and
	// the other fields are its buffers.
	gitSelectedRemote   string
	gitRemoteOpen       bool
	gitRemoteExisting   bool
	gitRemoteRemove     bool
	gitRemoteRemoveName string
	gitRemoteName       string
	gitRemoteURL        string
	gitRemotePick       gitSelectChoice
	gitBranchPick       gitSelectChoice
	gitBranchCreate     bool
	gitBranchName       string
	gitBranchDelete     bool
	gitBranchDeleteName string
	gitProfilesOpen     bool
	codeStats           core.CodeStats
	codeStatsFor        string
	codeStatsReady      bool
	codeStatsLoading    bool
	codeStatsCancel     context.CancelFunc
	codeStatsGeneration uint64
	gitBindID           string
	gitBind             bool
	gitBindKey          string
	gitBindLabel        string
	gitBindName         string
	gitBindEmail        string
	gitBindNote         string

	// Toolchains page.
	toolchainKind int // index into core.Kinds
	toolchainView int // 0: installed SDKs, 1: version managers
	managerList   ui.ListState
	installedList ui.ListState
	doctorList    ui.ListState

	// Downloads page.
	downloadKind  int
	remoteList    ui.ListState
	remote        []core.Remote
	remoteErr     string
	remoteLoading bool
	remoteKind    core.Kind // which kind remote refers to
	// remoteLoaded records that remote is a finished answer for remoteKind,
	// so the page can tell "not fetched yet" from "fetched, nothing there".
	remoteLoaded bool
	// remoteCache keeps each SDK's list, so switching the tab back and forth
	// does not refetch what is already here.
	remoteCache  map[core.Kind][]core.Remote
	remoteGroups map[core.Kind]remoteGroupedView
	filterLTS    bool
	// vendorPick is open while the user chooses which vendor build of a
	// version to download.
	vendorPick *vendorPick

	// Settings page.
	dataDir    string
	nodeMirror string
	themeMode  string
	followMark bool
	shellHook  bool

	// Dialog state.
	confirmRemove *core.Installed
	// confirmRemoveUsers lists the projects that still resolve to the version
	// in confirmRemove, so the confirmation can say what deletion breaks.
	confirmRemoveUsers []core.Project
	confirmForget      *core.Installed
	confirmDelete      *core.Project
	showEnvFor         int // project index whose environment is expanded, -1 none
	envOpen            bool
	// Adding a version that lives where discovery does not look.
	addOpen    bool
	addKindIdx int
	addPath    string
	addVer     string
	addNote    string
	addError   string

	// Derived per-project resolutions, rebuilt on the main thread.
	resolutions map[string][]core.Resolution
	// Doctor results, recomputed with them so every page sees one snapshot.
	report core.Report
	pinBuf []string

	jobs          []*Job
	tasks         taskService
	taskUpdate    func(func()) // Optional UI dispatcher for headless state tests.
	jobList       ui.ListState
	pendingToasts []string

	mu          sync.Mutex
	serviceSync *updateQueue
	refreshing  atomic.Bool
}

// New builds the app state around a service.
func New(svc *core.Service) *App {
	a := &App{svc: svc, selProject: -1, showEnvFor: -1,
		remoteCache: map[core.Kind][]core.Remote{}}
	cfg := svc.Config()
	a.dataDir = cfg.DataDir
	a.nodeMirror = cfg.NodeMirror
	a.themeMode = cfg.Theme
	a.followMark = cfg.FollowMarkers
	a.shellHook = cfg.ShellHook
	a.syncAndRecompute()
	a.settings = loadSettingsSnapshot(svc)
	return a
}

// SetWindow records the window the app draws into.
func (a *App) SetWindow(win *mygo.Window) {
	a.win = win
	// Capture the window once: background callbacks never read mutable UI state.
	a.watchService(win.Update, win.Invalidate)
	a.serviceSync.request()
}

// SetPage switches the visible page, for the command line's --page flag and
// for the snapshot tool.
func (a *App) SetPage(page int) {
	if page < 0 || page >= len(pageTitles) {
		return
	}
	a.page = page
	a.onPageEnter(page)
}

// OpenProject opens one project's configuration, for the snapshot tool.
func (a *App) OpenProject(index int, expandEmpty bool) {
	if index < 0 || index >= len(a.projects) {
		return
	}
	a.projectOpen = true
	a.selProject = index
	a.expandEmpty = expandEmpty
	a.showEnvFor = -1
	// A different project is a different repository, so the editor and its
	// message do not carry over.
	a.gitEdit = false
	a.gitBind = false
	a.gitRemoteOpen = false
	a.gitRemoteRemove = false
	a.gitBranchCreate = false
	a.gitBranchDelete = false
	a.gitProfilesOpen = false
	a.gitSelectedRemote = ""
	a.gitBindID = ""
	a.gitError = ""
	a.gitFor = ""
}

// OpenGitEditor opens the Git identity editor on the project page, for the
// OpenGitEditor opens the project-level identity editor, for the snapshot tool.
func (a *App) OpenGitEditor() {
	a.gitEdit = true
	a.seedGitBuffers()
}

// OpenGitBinding opens the remote-address binding editor, for the snapshot tool.
// It reads the repository itself rather than the frame's cached copy, so it works
// on the first frame, before the page has drawn once.
func (a *App) OpenGitBinding() {
	rows := a.projectRows()
	info := a.gitInfo
	if a.selProject >= 0 && a.selProject < len(rows) {
		if p, ok := a.svc.PeekGit(rows[a.selProject].Path); ok {
			info = p
		} else {
			info = a.svc.Git(rows[a.selProject].Path)
		}
	}
	a.gitInfo = info

	a.gitBind = true
	a.gitBindKey, _ = core.NormalizeRemote(info.Remote)
	a.gitBindLabel = ""
	a.gitBindName = info.Identity.Name
	a.gitBindEmail = info.Identity.Email
	a.gitBindID = ""
	if match := a.svc.MatchGitProfileFor(info.Remote); match != nil {
		a.gitBindID = match.IdentityID()
		a.gitBindKey = match.Key
		a.gitBindLabel = match.Label
		a.gitBindName = match.Name
		a.gitBindEmail = match.Email
	}
}

// OpenProjectByName opens a project by its directory name, for the snapshot
// tool: the list order follows recent use, so an index is not stable.
func (a *App) OpenProjectByName(name string, expandEmpty bool) bool {
	rows := a.projectRows()
	for i, p := range rows {
		if strings.EqualFold(p.Name, name) || strings.EqualFold(p.Path, name) {
			a.OpenProject(i, expandEmpty)
			return true
		}
	}
	return false
}

// SetRemote injects a download list, so a page can be rendered offline.
func (a *App) SetRemote(kind core.Kind, list []core.Remote) {
	a.remote = list
	a.remoteKind = kind
	a.remoteLoaded = true
	a.remoteLoading = false
	a.remoteErr = ""
	a.cacheRemote(kind, list)
}

// SetToolchainKind selects the SDK tab, for the snapshot tool.
func (a *App) SetToolchainKind(k core.Kind) {
	index := kindIndexOf(k)
	if a.toolchainKind != index {
		a.installedList = ui.ListState{}
	}
	a.toolchainKind = index
	a.toolchainView = 0
}

// OpenManagers opens the independent manager view, also used by offline previews.
func (a *App) OpenManagers() { a.page = PageToolchains; a.toolchainView = 1; a.requestServiceUpdate() }

// SetDownloadKind selects the download tab, for the snapshot tool.
func (a *App) SetDownloadKind(k core.Kind) { a.downloadKind = kindIndexOf(k) }

// DownloadsKind reports which SDK the download page is showing, for the
// snapshot tool.
func (a *App) DownloadsKind() core.Kind { return a.downloadsKind() }

// RemoteReady reports whether a kind's download list has arrived, so a
// non-interactive render can wait for the network instead of capturing an
// empty page.
func (a *App) RemoteReady(k core.Kind) bool {
	return a.remoteLoaded && a.remoteKind == k && len(a.remote) > 0
}

// OpenVendorPicker opens the download-source chooser for one version, for the
// snapshot tool. It behaves like the button does: a version with a single
// source has nothing to choose, so no dialog appears.
func (a *App) OpenVendorPicker(version string) bool {
	for _, g := range a.groupsFor(a.downloadsKind()) {
		if g.Version != version {
			continue
		}
		if !g.MultiVendor() {
			return false
		}
		a.vendorPick = &vendorPick{Group: g}
		return true
	}
	return false
}

// SetThemeMode overrides the appearance, for the snapshot tool.
func (a *App) SetThemeMode(mode string) { a.themeMode = mode }

// versionExample is a plausible version string for each SDK, used as the
// placeholder in the manual-add dialog.
func versionExample(kind core.Kind) string {
	switch kind {
	case core.Node:
		return "20.11.1"
	case core.Python:
		return "3.12.4"
	case core.Java:
		return "17.0.17"
	case core.Go:
		return "1.25.6"
	case core.Rust:
		return "1.80.0"
	case core.Ruby:
		return "3.3.6"
	}
	return ""
}

// managerLabel turns a manager id into something worth reading in the UI.
func managerLabel(manager string) string {
	switch manager {
	case "":
		return "未知来源"
	case "envmgr":
		return "EnvMgr 安装"
	case "GOTOOLCHAIN":
		return "Go 工具链缓存"
	case core.ManualManager:
		return "手动添加"
	case "PATH":
		return "系统 PATH"
	case "system":
		return "系统安装"
	}
	return manager
}

// syncNow copies the service's state into the app, on the main thread.
func (a *App) syncNow() {
	a.installed = a.svc.Installed()
	a.inventory = buildInventoryView(a.installed)
	a.projects = a.svc.Projects()
	a.managers = a.svc.Managers()
	a.defaults = a.svc.Defaults()
	a.syncedAt = nowLabel()
}

// refreshAsync rediscovers in the background and repaints when done.
func (a *App) refreshAsync() {
	if a.win == nil || !a.refreshing.CompareAndSwap(false, true) {
		return
	}
	win := a.win
	a.syncing = true
	win.Invalidate()
	go func() {
		// Refresh emits a change notification; its listener queues a snapshot,
		// never another Refresh. Repeated clicks share this in-flight scan.
		a.svc.Refresh()
		win.Update(func() {
			a.syncing = false
			a.refreshing.Store(false)
		})
	}()
}

// ---- theme ----

// palette is EnvMgr's own look: one accent, quiet surfaces, thin borders.
type palette struct {
	Dark     bool
	Bg       ui.Color
	Surface  ui.Color
	Hover    ui.Color
	Pressed  ui.Color
	Border   ui.Color
	Text     ui.Color
	Muted    ui.Color
	Accent   ui.Color
	AccentHi ui.Color
	OnAccent ui.Color
	Danger   ui.Color
	Warning  ui.Color
	Success  ui.Color
}

func (a *App) palette(dark bool) palette {
	if dark {
		return palette{
			Dark:     true,
			Bg:       ui.Hex("#0E1014"),
			Surface:  ui.Hex("#16191F"),
			Hover:    ui.Hex("#1D212A"),
			Pressed:  ui.Hex("#242935"),
			Border:   ui.Hex("#252A34"),
			Text:     ui.Hex("#E9ECF2"),
			Muted:    ui.Hex("#8B93A3"),
			Accent:   ui.Hex("#6C8CFF"),
			AccentHi: ui.Hex("#8AA3FF"),
			OnAccent: ui.Hex("#0B0E14"),
			Danger:   ui.Hex("#FF6B6B"),
			Warning:  ui.Hex("#F2B25C"),
			Success:  ui.Hex("#5FD1A0"),
		}
	}
	return palette{
		Bg:       ui.Hex("#F5F6F9"),
		Surface:  ui.Hex("#FFFFFF"),
		Hover:    ui.Hex("#F0F2F7"),
		Pressed:  ui.Hex("#E7EAF1"),
		Border:   ui.Hex("#E2E5EC"),
		Text:     ui.Hex("#14171E"),
		Muted:    ui.Hex("#69707F"),
		Accent:   ui.Hex("#4C6BFF"),
		AccentHi: ui.Hex("#3B58E8"),
		OnAccent: ui.Hex("#FFFFFF"),
		Danger:   ui.Hex("#E04A4A"),
		Warning:  ui.Hex("#C98A1E"),
		Success:  ui.Hex("#1F9E6F"),
	}
}

// theme derives the ui theme from the desktop appearance and the user's
// choice in settings.
func (a *App) theme(c *ui.Context) *ui.Theme {
	mode := a.themeMode
	// The desktop's appearance can only be read once the app is running;
	// offline renders (the snapshot tool) fall back to the explicit choice.
	if mode == "system" && !mygo.App.IsReady() {
		mode = "light"
	}
	var base *ui.Theme
	dark := false
	switch mode {
	case "dark":
		dark, base = true, ui.DarkTheme()
	case "light":
		dark, base = false, ui.LightTheme()
	default:
		dark = mygo.Theme.IsDark()
		base = ui.LightTheme()
		if dark {
			base = ui.DarkTheme()
		}
	}
	p := a.palette(dark)
	t := *base
	t.Dark = dark
	t.Background = p.Bg
	t.Surface = p.Surface
	t.SurfaceHover = p.Hover
	t.SurfacePressed = p.Pressed
	t.Border = p.Border
	t.Text = p.Text
	t.TextMuted = p.Muted
	t.Accent = p.Accent
	t.AccentHover = p.AccentHi
	t.AccentPressed = p.AccentHi
	t.AccentText = p.OnAccent
	t.Danger = p.Danger
	t.Warning = p.Warning
	t.Success = p.Success
	t.Inverse = p.Pressed
	t.InverseText = p.Text
	t.Selection = p.Accent
	t.Focus = p.Accent
	t.Radius = 9
	t.Spacing = 4
	t.ScrollbarWidth = 8
	return t.WithPreferences(c.Preferences())
}

// ---- view ----

// View is the window's content: the view function MyGo calls every frame.
func (a *App) View(c *ui.Context) { a.view(c) }

// view builds the whole window from the state, once per frame.
func (a *App) view(c *ui.Context) {
	t := a.theme(c)
	c.SetTheme(t)

	root := ui.Row(c).Fill().AlignItems(ui.Stretch).Background(t.Background)
	root.Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.header(c)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				switch a.page {
				case PageDashboard:
					a.dashboard(c)
				case PageProjects:
					a.projectsPage(c)
				case PageToolchains:
					a.toolchainsPage(c)
				case PageDownloads:
					a.downloadsPage(c)
				case PageDoctor:
					a.doctorPage(c)
				case PageSettings:
					a.settingsPage(c)
				}
			})
		})
	})

	a.dialogs(c)
	a.jobsBar(c)
}

// sidebar is the left rail: identity, navigation, and a status footer.
func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	side := ui.Column(c).Width(224).Shrink(0).FillHeight().
		Background(t.Surface).
		BorderWidth(0, 1, 0, 0).BorderColor(t.Border).
		Padding(16, 12, 12, 12).Gap(4)

	side.Children(func() {
		// Identity
		ui.Row(c).Gap(10).Padding(6, 4, 14, 4).Children(func() {
			ui.Box(c).Size(28, 28).Radius(8).Background(t.Accent).Center().Children(func() {
				ui.Icon(c, icLogo).Size(17, 17).TextColor(t.AccentText)
			})
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, "EnvMgr").FontSize(14).Bold().SingleLine()
				ui.Text(c, "项目环境管理").FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
		})

		for i, p := range pageTitles {
			a.navItem(c, i, p.Title, p.Icon)
		}

		ui.Spacer(c)

		// Status footer: what the last scan found.
		ui.Column(c).Gap(6).Padding(10, 10).Radius(8).Background(t.Background).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(t.Success)
				ui.Text(c, fmt.Sprintf("%d 个项目 · %d 个版本", len(a.projects), len(a.installed))).
					FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			ui.Text(c, "更新于 "+a.syncedAt).FontSize(10).TextColor(t.TextMuted).SingleLine()
		})
	})
}

// navItem is one row of the left rail.
func (a *App) navItem(c *ui.Context, index int, label string, icon *ui.SVG) {
	t := c.Theme()
	selected := a.page == index
	row := ui.Row(c).Gap(10).Padding(8, 10).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	switch {
	case selected:
		row.Background(t.Accent)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		fg := t.Text
		if selected {
			fg = t.AccentText
		}
		ui.Icon(c, icon).Size(16, 16).TextColor(fg)
		ui.Text(c, label).FontSize(13).SingleLine().TextColor(fg)
		if selected {
			ui.Text(c, " ").FontSize(13)
		}
	})
	if row.Clicked() {
		a.page = index
		a.onPageEnter(index)
	}
}

// onPageEnter kicks off the work a page needs when it is opened.
func (a *App) onPageEnter(index int) {
	switch index {
	case PageDownloads:
		// Only ask the feeds when the list is not already here.
		if len(a.remote) == 0 || a.remoteKind != a.downloadsKind() {
			a.loadRemote(a.downloadsKind(), false)
		}
	case PageToolchains, PageProjects, PageDoctor, PageSettings:
		a.requestServiceUpdate()
	}
}

// header is the title bar of the content column.
func (a *App) header(c *ui.Context) {
	t := c.Theme()
	p := pageTitles[a.page]
	ui.Row(c).Padding(20, 20, 14, 18).Gap(12).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, p.Title).FontSize(18).Bold().SingleLine()
			ui.Text(c, p.Desc).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
		a.headerActions(c)
	})
}

// headerActions draws the buttons that belong to the current page.
func (a *App) headerActions(c *ui.Context) {
	t := c.Theme()
	switch a.page {
	case PageProjects:
		if a.button(c, icPlus, "添加项目").Clicked() {
			a.addProject()
		}
	case PageToolchains, PageDashboard:
		if a.button(c, icRefresh, "重新扫描").Clicked() {
			a.refreshAsync()
			c.Toast("正在重新扫描本机 SDK")
		}
	case PageDoctor:
		if a.button(c, icRefresh, "重新体检").Clicked() {
			a.refreshAsync()
			a.recompute()
			c.Toast("正在重新体检")
		}
	}
	_ = t
}

// button is a quiet icon+label button used in headers and rows.
func (a *App) button(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	t := c.Theme()
	btn := ui.Button(c, "").Padding(5, 10).Radius(7).Border(1, t.Border)
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	btn.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(t.Text)
			ui.Text(c, label).FontSize(12).TextColor(t.Text).SingleLine()
		})
	})
	btn.Label(label)
	return btn
}

// iconButton is a square button holding only an icon.
func (a *App) iconButton(c *ui.Context, icon *ui.SVG, label string, danger bool) ui.Element {
	t := c.Theme()
	fg := t.Text
	if danger {
		fg = t.Danger
	}
	btn := ui.Button(c, "").Size(28, 28).Padding(0).Gap(0).Shrink(0).Radius(7).Center()
	if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
	btn.Children(func() { ui.Icon(c, icon).Size(14, 14).Shrink(0).TextColor(fg) })
	btn.Label(label).Tooltip(label)
	return btn
}

// ---- small pieces used by every page ----

// card is EnvMgr's surface: rounded, thin border, a little shadow.
func (a *App) card(c *ui.Context) ui.Element {
	t := c.Theme()
	e := ui.Column(c).Radius(12).Background(t.Surface).Border(1, t.Border).Padding(16)
	if !t.Dark {
		e.Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04))
	}
	return e
}

// sectionLabel is the small caps line above a group of controls.
func (a *App) sectionLabel(c *ui.Context, text string) {
	t := c.Theme()
	ui.Text(c, text).FontSize(11).Bold().TextColor(t.TextMuted).SingleLine()
}

// chip is a small rounded tag, e.g. "nvm" or "LTS".
func (a *App) chip(c *ui.Context, text string, fg, bg ui.Color) {
	ui.Box(c).Padding(2, 7).Radius(6).Background(bg).Children(func() {
		ui.Text(c, text).FontSize(10).TextColor(fg).SingleLine()
	})
}

// kindChip shows one SDK version with its accent colour.
func (a *App) kindChip(c *ui.Context, kind core.Kind, version string, missing bool) {
	t := c.Theme()
	info := core.Info(kind)
	fg := ui.Hex(info.Color)
	bg := fg.Alpha(0.14)
	if missing {
		fg, bg = t.Danger, t.Danger.Alpha(0.12)
	}
	ui.Box(c).Padding(2, 7).Radius(6).Background(bg).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, info.Name).FontSize(10).TextColor(fg).SingleLine()
			ui.Text(c, version).FontSize(10).Bold().TextColor(fg).SingleLine()
		})
	})
}

// eyebrow is the label above a big number.
func (a *App) eyebrow(c *ui.Context, text string) {
	t := c.Theme()
	ui.Text(c, text).FontSize(11).TextColor(t.TextMuted).SingleLine()
}

// emptyState fills a page area when there is nothing to show.
func (a *App) emptyState(c *ui.Context, icon *ui.SVG, title, body, action string) bool {
	t := c.Theme()
	clicked := false
	ui.Column(c).Fill().Center().Gap(10).Children(func() {
		ui.Box(c).Size(44, 44).Radius(12).Background(t.SurfaceHover).Center().Children(func() {
			ui.Icon(c, icon).Size(20, 20).TextColor(t.TextMuted)
		})
		ui.Text(c, title).FontSize(14).Bold()
		ui.Text(c, body).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center).
			MaxWidth(360).Wrap()
		if action != "" {
			if ui.PrimaryButton(c, action).Clicked() {
				clicked = true
			}
		}
	})
	return clicked
}

// ---- helpers ----

// kindIndexOf maps a kind to its position in the segmented controls.
func kindIndexOf(kind core.Kind) int {
	for i, k := range core.Kinds {
		if k == kind {
			return i
		}
	}
	return 0
}

// sortedInstalled returns the versions of a kind, newest first.
func (a *App) sortedInstalled(kind core.Kind) []core.Installed {
	return a.inventory.list(kind)
}

// projectRows is the project list filtered by the search field.
func (a *App) projectRows() []core.Project {
	if strings.TrimSpace(a.projectQuery) == "" {
		return a.projects
	}
	q := strings.ToLower(strings.TrimSpace(a.projectQuery))
	out := make([]core.Project, 0, len(a.projects))
	for _, p := range a.projects {
		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Path), q) {
			out = append(out, p)
		}
	}
	return out
}
