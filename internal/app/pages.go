package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"envmgr/internal/core"
)

// ---- 概览 ----

func (a *App) dashboard(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Fill().Padding(20, 20, 22, 20).Gap(16).Children(func() {
		a.hero(c)

		// Four numbers, the state of this machine in one look.
		missing := a.missingCount()
		ui.Grid(c).Columns(4).Gap(12).Children(func() {
			a.statCard(c, "项目", fmt.Sprint(len(a.projects)), "在 EnvMgr 里固定版本")
			a.statCard(c, "已装版本", fmt.Sprint(len(a.installed)), "来自本机所有管理器")
			a.statCard(c, "待处理", fmt.Sprint(missing), "项目要的版本还没装")
			a.statCard(c, "版本管理器", fmt.Sprint(a.managerCount()), "nvm、pyenv、fnm 等")
		})

		// A one-line configuration checkup, with the way into the details.
		a.checkupStrip(c)

		ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
			// Recent projects.
			recent := a.card(c).Grow(1).Gap(12)
			recent.Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "最近项目").FontSize(14).Bold().Grow(1)
					if len(a.projects) > 0 {
						if ui.Button(c, "").Padding(2, 8).Children(func() {
							ui.Text(c, "全部").FontSize(11).TextColor(t.TextMuted)
						}).Label("全部项目").Clicked() {
							a.page = PageProjects
						}
					}
				})
				if len(a.projects) == 0 {
					if a.emptyState(c, icFolder, "还没有项目", "添加一个项目目录，EnvMgr 会读取它的 .nvmrc、.python-version 等文件。", "添加项目") {
						a.addProject()
					}
					return
				}
				for i, p := range a.projects {
					if i >= 5 {
						break
					}
					a.projectRow(c, p, i)
				}
			})

			// SDK inventory.
			inv := a.card(c).Width(300).Shrink(0).Gap(10)
			inv.Children(func() {
				ui.Text(c, "SDK 概览").FontSize(14).Bold()
				for _, kind := range core.Kinds {
					info := core.Info(kind)
					list := a.sortedInstalled(kind)
					row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(4, 6).Radius(6)
					if row.Hovered() {
						row.Background(t.SurfaceHover)
					}
					row.Children(func() {
						ui.Box(c).Size(8, 8).Radius(4).Background(ui.Hex(info.Color))
						ui.Text(c, info.Name).FontSize(12).Grow(1).SingleLine()
						if len(list) == 0 {
							ui.Text(c, "未安装").FontSize(11).TextColor(t.TextMuted)
						} else {
							ui.Text(c, fmt.Sprintf("%d 个", len(list))).FontSize(11).TextColor(t.TextMuted)
							ui.Text(c, list[0].Version).FontSize(11).Bold().SingleLine()
						}
					})
					if row.Clicked() {
						a.page = PageToolchains
						a.SetToolchainKind(kind)
					}
				}
			})
		})
	})
}

// checkupStrip is the overview's short version of the doctor page.
func (a *App) checkupStrip(c *ui.Context) {
	t := c.Theme()
	rep := a.report
	fg, icon := t.Success, icCheck
	if rep.Fail > 0 {
		fg, icon = t.Danger, icAlert
	} else if rep.Warn > 0 {
		fg, icon = t.Warning, icAlert
	}
	row := a.card(c).Padding(12, 16)
	row.Padding(11, 16)
	row.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(28, 28).Radius(8).Background(fg.Alpha(0.12)).Center().Children(func() {
				ui.Icon(c, icon).Size(15, 15).TextColor(fg)
			})
			ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "配置体检 · "+rep.Summary()).FontSize(12).
					TextColor(t.Text).SingleLine().Ellipsis("…")
				ui.Text(c, "运行环境、存储、终端钩子、工具链、项目、设置逐项检查").
					FontSize(10.5).TextColor(t.TextMuted).SingleLine()
			})
			if outdated, ended := a.updateCount(); outdated > 0 || ended > 0 {
				text := fmt.Sprintf("%d 个版本可更新", outdated)
				if outdated == 0 {
					text = fmt.Sprintf("%d 个系列已停止维护", ended)
				}
				if ui.Button(c, text).Label("查看可更新或已停止维护的版本").Clicked() {
					a.SetPage(PageToolchains)
					a.toolchainView = 0
				}
			}
			if ui.Button(c, "查看详情").Label("查看详情").Clicked() {
				a.SetPage(PageDoctor)
			}
		})
	})
}

// hero is the banner at the top of the overview.
func (a *App) hero(c *ui.Context) {
	t := c.Theme()
	e := ui.Column(c).Padding(22, 22).Radius(14).Gap(6)
	if t.Dark {
		e.Gradient(ui.Hex("#1B2340"), ui.Hex("#141824"), 135)
	} else {
		e.Gradient(ui.Hex("#EEF2FF"), ui.Hex("#F7F9FF"), 135)
	}
	e.Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(6).Grow(1).Children(func() {
				ui.Text(c, "项目的 SDK 版本，一处管好").FontSize(19).Bold().SingleLine()
				ui.Text(c, "读取项目已有的 .nvmrc / .python-version / .java-version，进入目录自动切换；不动全局环境变量。").
					FontSize(12).TextColor(t.TextMuted).MaxLines(2).Wrap()
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, "添加项目").Label("添加项目").Clicked() {
					a.addProject()
				}
				if ui.Button(c, "安装新版本").Label("安装新版本").Clicked() {
					a.page = PageDownloads
					a.loadRemote(a.downloadsKind(), false)
				}
			})
		})
	})
}

// statCard is one of the four numbers.
func (a *App) statCard(c *ui.Context, label, value, note string) {
	t := c.Theme()
	card := a.card(c).Gap(2).Padding(14, 16)
	card.Children(func() {
		a.eyebrow(c, label)
		ui.Text(c, value).FontSize(24).Bold().SingleLine()
		ui.Text(c, note).FontSize(10).TextColor(t.TextMuted).SingleLine()
	})
}

// projectRow is one line in the recent list.
func (a *App) projectRow(c *ui.Context, p core.Project, index int) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).Padding(8, 10).Radius(8).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Box(c).Size(26, 26).Radius(7).Background(t.SurfaceHover).Center().Children(func() {
			ui.Icon(c, icFolder).Size(14, 14).TextColor(t.TextMuted)
		})
		ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, p.Name).FontSize(12.5).Bold().SingleLine()
			ui.Text(c, p.Path).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		res := a.resolutions[p.ID]
		for _, r := range res {
			version := "未装 " + r.Expr
			if r.Installed != nil {
				version = r.Installed.Version
			}
			a.kindChip(c, r.Kind, version, r.Missing)
		}
		if a.iconButton(c, icTerminal, "打开终端", false).Clicked() {
			a.openTerminal(p)
		}
	})
	if row.Clicked() {
		a.page = PageProjects
		a.selProject = index
		a.showEnvFor = -1
	}
}

// ---- 项目 ----

func (a *App) projectsPage(c *ui.Context) {
	if len(a.projects) == 0 {
		if a.emptyState(c, icFolder, "还没有项目", "选择项目目录，EnvMgr 读取它的版本声明文件；也可以固定任意 SDK 版本。", "添加项目") {
			a.addProject()
		}
		return
	}
	if a.projectOpen {
		a.projectWorkspace(c)
		return
	}
	a.projectGallery(c)
}

// projectCard is one tile of the project wall: identity, what it pins, and
// whether anything is missing.
func (a *App) projectCard(c *ui.Context, p core.Project, index int) {
	t := c.Theme()
	res := a.resolutions[p.ID]
	health := core.Health(res)
	card := a.card(c).Padding(14).Gap(9)
	if card.Hovered() {
		card.BorderColor(t.Accent.Alpha(0.4))
	}
	card.Children(func() {
		ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(28, 28).Radius(8).Background(t.SurfaceHover).Center().Children(func() {
				ui.Icon(c, icFolder).Size(15, 15).TextColor(t.TextMuted)
			})
			ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, p.Name).FontSize(13).Bold().SingleLine().Ellipsis("…")
				ui.Text(c, shortPath(p.Path)).FontSize(10).TextColor(t.TextMuted).
					SingleLine().Ellipsis("…")
			})
			if health.Missing > 0 {
				ui.Icon(c, icAlert).Size(13, 13).TextColor(t.Warning)
			}
		})

		// Up to three versions, then a count, so the tile height stays even.
		if len(res) == 0 {
			ui.Text(c, "未配置 SDK").FontSize(11).TextColor(t.TextMuted).SingleLine()
		} else {
			ui.Row(c).Gap(5).Children(func() {
				for i, r := range res {
					if i >= 3 {
						a.chip(c, fmt.Sprintf("+%d", len(res)-3), t.TextMuted, t.SurfaceHover)
						return
					}
					version := r.Expr
					if r.Installed != nil {
						version = r.Installed.Version
					}
					a.kindChip(c, r.Kind, version, r.Missing)
				}
			})
		}

		// The commit address, so a tile names the identity a commit would use
		// without opening the project. A binding's own name is shown when it has
		// one, because "公司账号" reads faster than a URL.
		if git, ok := a.svc.PeekGit(p.Path); ok && git.Available {
			ui.Row(c).Gap(5).Wrap().Children(func() {
				for _, remote := range git.Remotes {
					label, _ := a.gitAddressLabel(remote.URL)
					if remote.Name != "origin" {
						label = remote.Name
					}
					a.chip(c, label, t.TextMuted, t.SurfaceHover)
				}
			})
		}

		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if health.Missing > 0 {
				ui.Text(c, fmt.Sprintf("%d 个版本未安装", health.Missing)).
					FontSize(10).TextColor(t.Warning).SingleLine()
			} else if len(res) > 0 {
				ui.Text(c, "进入目录即生效").FontSize(10).TextColor(t.Success).SingleLine()
			} else {
				ui.Text(c, "点开配置版本").FontSize(10).TextColor(t.TextMuted).SingleLine()
			}
		})
	})
	if card.Clicked() {
		a.selProject = index
		a.projectOpen = true
		a.expandEmpty = false
		a.showEnvFor = -1
	}
}

// projectGallery is the wall of every project, four to a row.
func (a *App) projectGallery(c *ui.Context) {
	t := c.Theme()
	rows := a.projects
	ui.Scroll(c).Fill().Padding(20, 20, 24, 20).Gap(14).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("%d 个项目", len(rows))).FontSize(13).Bold()
			if n := a.missingCount(); n > 0 {
				a.chip(c, fmt.Sprintf("%d 个版本未安装", n), t.Warning, t.Warning.Alpha(0.12))
			} else {
				a.chip(c, "全部就绪", t.Success, t.Success.Alpha(0.12))
			}
			ui.Spacer(c)
			ui.Text(c, "点一张卡片，打开它的配置").FontSize(10.5).TextColor(t.TextMuted)
		})
		ui.Grid(c).Columns(4).Gap(12).Children(func() {
			for i, p := range rows {
				a.projectCard(c, p, i)
			}
		})
	})
}

// projectWorkspace is one project open: the wall shrinks to a list on the
// left, the configuration fills the right.
func (a *App) projectWorkspace(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.projectSidebar(c)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.projectConfig(c)
		})
	})
}

// projectSidebar is the collapsed list, still searchable so a big wall stays
// navigable once it is out of sight.
func (a *App) projectSidebar(c *ui.Context) {
	t := c.Theme()
	rows := a.projectRows()
	pane := ui.Column(c).Width(248).FillHeight().Background(t.Surface).
		BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Padding(12, 10).Gap(8)
	pane.Children(func() {
		back := ui.Button(c, "").Padding(5, 8).Radius(7).Width(ui.Auto)
		if back.Hovered() {
			back.Background(t.SurfaceHover)
		}
		back.Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icChevron).Size(12, 12).TextColor(t.TextMuted)
				ui.Text(c, "全部项目").FontSize(11.5).TextColor(t.Text).SingleLine()
			})
		})
		back.Label("返回项目列表")
		if back.Clicked() {
			a.projectOpen = false
		}
		ui.SearchField(c, &a.projectQuery).Placeholder("搜索项目").Label("搜索项目")
		if a.selProject >= len(rows) && len(rows) > 0 {
			a.selProject = len(rows) - 1
		}
		a.projectList.Key = func(i int) any { return rows[i].ID }
		a.projectList.Selected = &a.selProject
		ui.List(c, &a.projectList, len(rows), func(i int) {
			p := rows[i]
			row := ui.Row(c).Gap(8).Padding(7, 8).Radius(7).AlignItems(ui.Center)
			if row.Hovered() && i != a.selProject {
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				ui.Box(c).Size(22, 22).Radius(6).Background(t.SurfaceHover).Center().Children(func() {
					ui.Icon(c, icFolder).Size(12, 12).TextColor(t.TextMuted)
				})
				ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, p.Name).FontSize(12).SingleLine().Ellipsis("…")
					ui.Text(c, shortPath(p.Path)).FontSize(9.5).TextColor(t.TextMuted).
						SingleLine().Ellipsis("…")
				})
				if h := core.Health(a.resolutions[p.ID]); h.Missing > 0 {
					ui.Icon(c, icAlert).Size(11, 11).TextColor(t.Warning)
				}
			})
			if row.RowClicked() {
				a.selProject = i
				a.expandEmpty = false
				a.showEnvFor = -1
			}
		}).Grow(1).Children(func() {
			if len(rows) == 0 {
				ui.Text(c, "没有匹配的项目").FontSize(11).TextColor(t.TextMuted).Padding(8)
			}
		})
	})
}

// projectConfig is the right side: three columns of SDK cards, with the SDKs
// this project does not configure folded away until asked for.
func (a *App) projectConfig(c *ui.Context) {
	rows := a.projectRows()
	if len(rows) == 0 {
		a.emptyState(c, icSearch, "没有匹配的项目", "换一个搜索词，或返回项目列表。", "返回")
		return
	}
	if a.selProject < 0 || a.selProject >= len(rows) {
		a.selProject = 0
	}
	p := rows[a.selProject]
	res := a.resolutions[p.ID]
	t := c.Theme()

	// Split the kinds into what this project configures and what it does not.
	var configured, empty []core.Kind
	for _, kind := range core.Kinds {
		r, ok := findResolution(res, kind)
		if len(a.sortedInstalled(kind)) == 0 && !ok {
			continue // nothing to offer for this SDK at all
		}
		if ok && (r.Expr != "" || r.Installed != nil) {
			configured = append(configured, kind)
			continue
		}
		empty = append(empty, kind)
	}

	ui.Scroll(c).Fill().Padding(20, 20, 24, 20).Gap(14).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, p.Name).FontSize(17).Bold().SingleLine()
				ui.Text(c, p.Path).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			if job := a.jobFor(projectJobID(p)); job != nil && job.Running {
				ui.Text(c, "正在就绪 · "+job.Stage).FontSize(11).TextColor(t.TextMuted)
				if !job.Cancelling && ui.Button(c, "取消就绪").Clicked() {
					a.cancelJob(job)
				}
			} else if ui.PrimaryButton(c, "一键就绪").Clicked() {
				a.readyProject(p)
			}
			if a.button(c, icTerminal, "终端").Label("在终端中打开").Clicked() {
				a.openTerminal(p)
			}
			if a.button(c, icExternal, "目录").Label("在文件管理器中显示").Clicked() {
				openPath(p.Path)
			}
			if a.iconButton(c, icTrash, "移除项目", true).Clicked() {
				a.confirmDelete = &p
			}
		})

		if job := a.jobFor(projectJobID(p)); job != nil {
			a.card(c).Children(func() { a.jobRow(c, job) })
		}
		for _, r := range res {
			if r.Err != nil {
				ui.Text(c, "项目配置错误："+r.Err.Error()).TextColor(t.Danger).Wrap()
			}
		}

		// Three columns: the SDKs this project actually uses.
		if len(configured) > 0 {
			ui.Grid(c).Columns(3).Gap(12).Children(func() {
				for _, kind := range configured {
					r, _ := findResolution(res, kind)
					a.kindCard(c, p, kind, r, a.sortedInstalled(kind), false)
				}
			})
		} else {
			ui.Column(c).Padding(16).Radius(10).Background(t.SurfaceHover).Gap(4).Children(func() {
				ui.Text(c, "这个项目还没有配置任何 SDK").FontSize(12).Bold()
				ui.Text(c, "EnvMgr 会读取 .nvmrc、.node-version、.python-version、.java-version、go.mod、rust-toolchain 与 .tool-versions；也可以在下面直接固定版本。").
					FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(520)
			})
		}

		// The rest, folded away: they are available but not what this project
		// asks for, so they stay quiet until the user wants them.
		if len(empty) > 0 {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "未配置").FontSize(12).Bold().TextColor(t.TextMuted)
				a.chip(c, fmt.Sprint(len(empty)), t.TextMuted, t.SurfaceHover)
				ui.Text(c, "这些 SDK 本机已装，但这个项目没有用到").
					FontSize(10.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
				ui.Spacer(c)
				label := "展开"
				if a.expandEmpty {
					label = "收起"
				}
				if a.button(c, icChevron, label).Clicked() {
					a.expandEmpty = !a.expandEmpty
				}
			})
			if a.expandEmpty {
				ui.Grid(c).Columns(3).Gap(12).Children(func() {
					for _, kind := range empty {
						r, _ := findResolution(res, kind)
						a.kindCard(c, p, kind, r, a.sortedInstalled(kind), true)
					}
				})
			}
		}

		// Git: whether this directory is a repository, and whose name the next
		// commit would carry.
		a.gitPanel(c, p)

		// Environment preview.
		env := core.BuildEnv(res)
		envCard := a.card(c).Gap(8)
		envCard.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "注入到终端的环境").FontSize(12).Bold().Grow(1)
				if ui.Button(c, "").Padding(2, 8).Children(func() {
					ui.Text(c, "复制命令").FontSize(11).TextColor(t.TextMuted)
				}).Label("复制激活命令").Clicked() {
					if err := env.Validate(); err != nil {
						a.pushToast("项目环境尚未就绪：" + err.Error())
					} else {
						c.WriteClipboard(env.Export(core.CurrentShell()))
						c.Toast("已复制激活命令，粘贴到终端即可进入该项目的环境")
					}
				}
			})
			if err := env.Validate(); err != nil {
				ui.Text(c, err.Error()).FontSize(11).TextColor(t.Danger).Wrap()
				return
			}
			if env.Empty() {
				ui.Text(c, "没有需要注入的变量：这个项目还没有可用的 SDK 版本。").
					FontSize(11).TextColor(t.TextMuted).Wrap()
				return
			}
			for _, line := range env.Describe() {
				ui.Text(c, line).FontSize(11).Font("monospace").TextColor(t.TextMuted).
					SingleLine().Ellipsis("…")
			}
		})
	})
}

// kindCard is one SDK in the project's configuration: what it resolves to and
// the picker to change it. A card for an SDK the project does not configure is
// drawn faded, so it reads as available but unused.
func (a *App) kindCard(c *ui.Context, p core.Project, kind core.Kind, r core.Resolution, installed []core.Installed, faded bool) {
	t := c.Theme()
	info := core.Info(kind)
	card := a.card(c).Padding(13).Gap(8)
	label := t.Text
	muted := t.TextMuted
	dot := ui.Hex(info.Color)
	if faded {
		card.Background(t.SurfaceHover)
		label = t.TextMuted
		muted = t.TextMuted.Alpha(0.75)
		dot = dot.Alpha(0.35)
	}
	card.Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(dot)
			ui.Text(c, info.Name).FontSize(12.5).Bold().TextColor(label)
			if r.Expr != "" {
				src := r.Source
				if src == "" {
					src = "pin"
				}
				a.chip(c, src, muted, t.Surface)
			}
			ui.Spacer(c)
			switch {
			case r.Missing:
				ui.Icon(c, icAlert).Size(12, 12).TextColor(t.Warning)
			case r.Installed != nil:
				ui.Icon(c, icCheck).Size(12, 12).TextColor(t.Success.Alpha(0.8))
			}
		})

		// What it currently resolves to.
		switch {
		case r.Missing:
			ui.Text(c, "未安装 "+r.Expr).FontSize(11).TextColor(t.Warning).SingleLine()
		case r.Installed != nil:
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.Installed.Version).FontSize(11.5).Bold().TextColor(label).SingleLine()
				if r.Installed.Manager != "" && r.Installed.Manager != "envmgr" {
					ui.Text(c, "· "+r.Installed.Manager).FontSize(10).TextColor(muted).SingleLine()
				}
			})
		default:
			ui.Text(c, "未配置").FontSize(11).TextColor(muted).SingleLine()
		}

		if r.Missing {
			if ui.Button(c, "去安装").Label("去安装").Clicked() {
				a.page = PageDownloads
				a.downloadKind = kindIndexOf(kind)
				a.loadRemote(kind, false)
			}
		}

		// The picker: follow the project's own file, or pin a version.
		options, values := versionOptions(kind, r, installed)
		idx := kindIndexOf(kind)
		if len(a.pinBuf) == len(core.Kinds) {
			a.pinBuf[idx] = options[0]
			for i, v := range values {
				if v != "" && r.Source == "pin" && v == r.Expr {
					a.pinBuf[idx] = options[i]
				}
			}
			pick := selectChoice{Name: a.pinBuf[idx], Value: a.pinBuf[idx]}
			choices := make([]selectChoice, len(options))
			for i, option := range options {
				choices[i] = selectChoice{Name: option, Value: option}
			}
			if a.selectControl(c, &pick, choices, info.Name+" 版本", false, false) {
				for i, opt := range options {
					if opt == pick.Name {
						a.pinBuf[idx] = opt
						a.setPin(p, kind, values[i])
						break
					}
				}
			}
		}
	})
}

// ---- 工具链 ----

func (a *App) toolchainsPage(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(20, 20, 24, 20).Gap(14).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Segmented(c, &a.toolchainView, "已装 SDK", "版本管理器").Label("工具链视图")
			ui.Spacer(c)
			if a.updatesBusy {
				ui.Text(c, "正在检查更新…").FontSize(11).TextColor(t.TextMuted)
			} else if ui.Button(c, "检查更新").Label("检查已装版本有没有新版本、是否已停止维护").Clicked() {
				a.loadUpdates(true)
			}
		})
		if a.toolchainView == 1 {
			a.toolchainManagers(c)
			return
		}
		a.toolchainVersions(c)
	})
}

func (a *App) toolchainVersions(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Segmented(c, &a.toolchainKind, kindNames()...).Label("SDK 类型").Changed() {
			a.installedList = ui.ListState{}
		}
		ui.Spacer(c)
		if ui.Button(c, "＋ 添加已安装版本").Label("手动添加一个不在常规位置的版本").Clicked() {
			a.openAddVersion()
		}
	})
	kind := core.Kinds[a.toolchainKind]
	list := a.sortedInstalled(kind)
	rows := len(list)
	if rows == 0 {
		rows = 1
	}
	a.installedList.Key = func(i int) any {
		if len(list) == 0 {
			return "empty:" + string(kind)
		}
		inst := list[i]
		return "installed:" + string(inst.Kind) + "@" + inst.Version + ":" + inst.Path
	}
	a.card(c).Fill().Gap(0).Padding(6).Children(func() {
		ui.List(c, &a.installedList, rows, func(i int) {
			if len(list) > 0 {
				a.installedRow(c, list[i])
				return
			}
			ui.Column(c).Padding(20).Gap(8).Center().Children(func() {
				ui.Icon(c, icBox).Size(22, 22).TextColor(t.TextMuted)
				ui.Text(c, "本机没有 "+core.Info(kind).Name+" 的任何版本").FontSize(12.5).Bold()
				ui.Text(c, "可直接下载 SDK，或到「版本管理器」安装管理工具。").FontSize(11).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "去下载").Clicked() {
						a.page = PageDownloads
						a.downloadKind = kindIndexOf(kind)
						a.loadRemote(kind, false)
					}
					if ui.Button(c, "手动添加").Clicked() {
						a.openAddVersion()
					}
					if ui.Button(c, "查看版本管理器").Clicked() {
						a.OpenManagers()
					}
				})
			})
		}).Grow(1)
	})
}

func (a *App) toolchainManagers(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "版本管理器").FontSize(13).Bold()
		a.chip(c, fmt.Sprintf("%d 个", len(a.managers)), t.TextMuted, t.SurfaceHover)
	})
	ui.Text(c, "集中管理 nvm、fnm、pyenv、mise 等工具；支持多种语言的管理器统一显示一次。").FontSize(11).TextColor(t.TextMuted).Wrap()
	a.managerList.Key = func(i int) any { return "manager:" + a.managers[i].ID }
	a.card(c).Fill().Gap(0).Padding(6).Children(func() {
		if len(a.managers) == 0 {
			ui.Text(c, "暂时没有版本管理器信息，可点击上方「重新扫描」更新。").FontSize(11).TextColor(t.TextMuted)
			return
		}
		ui.List(c, &a.managerList, len(a.managers), func(i int) { a.managerRow(c, a.managers[i]) }).Grow(1)
	})
}

// installedRow shows one SDK version.
func (a *App) installedRow(c *ui.Context, inst core.Installed) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).Padding(9, 10).Radius(7).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Column(c).Gap(2).Width(120).Shrink(0).Children(func() {
			ui.Text(c, inst.Version).FontSize(12.5).Bold().SingleLine()
			ui.Text(c, core.Info(inst.Kind).Name).FontSize(10).TextColor(t.TextMuted).SingleLine()
		})
		a.chip(c, managerLabel(inst.Manager), t.TextMuted, t.SurfaceHover)
		a.updatesBadge(c, inst)
		if u, ok := a.updateFor(inst); ok && u.Outdated() && u.Latest != "" {
			if inst.Manager == "envmgr" && inst.Kind != core.Rust {
				if ui.Button(c, "升级到 "+u.Latest).Label("升级 " + inst.ID() + " 到 " + u.Latest).Clicked() {
					a.startUpgrade(u)
				}
			} else {
				a.chip(c, "可更新 "+u.Latest, t.Accent, t.Accent.Alpha(0.12))
			}
		}
		ui.Text(c, inst.Path).FontSize(10.5).Font("monospace").TextColor(t.TextMuted).
			Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if a.iconButton(c, icCopy, "复制路径", false).Clicked() {
			c.WriteClipboard(inst.Path)
			c.Toast("已复制 " + inst.Path)
		}
		if a.iconButton(c, icExternal, "在文件管理器中显示", false).Clicked() {
			openPath(inst.Path)
		}
		switch inst.Manager {
		case "envmgr", "rustup":
			if a.iconButton(c, icTrash, "删除该版本", true).Clicked() {
				a.confirmRemove = &inst
				a.confirmRemoveUsers = a.svc.VersionUsers(inst.Kind, inst.Version)
			}
		case core.ManualManager:
			// EnvMgr did not create this directory, so the most it can do is
			// stop listing it.
			if a.iconButton(c, icTrash, "从列表移除（不删磁盘文件）", true).Clicked() {
				a.confirmForget = &inst
			}
		}
	})
}

// managerRow shows one version manager and whether it is here.
func (a *App) managerRow(c *ui.Context, m core.ManagerState) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).Padding(9, 10).Radius(7).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Column(c).Gap(2).Width(150).Shrink(0).Children(func() {
			ui.Text(c, m.Name).FontSize(12.5).Bold().SingleLine()
			ui.Text(c, m.Desc).FontSize(10).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		ui.Row(c).Gap(4).Children(func() {
			for i, k := range m.Kinds {
				if i == 3 {
					a.chip(c, fmt.Sprintf("+%d", len(m.Kinds)-3), t.TextMuted, t.SurfaceHover)
					break
				}
				a.chip(c, core.Info(k).Name, ui.Hex(core.Info(k).Color), ui.Hex(core.Info(k).Color).Alpha(0.12))
			}
		}).Width(180).Shrink(0)
		ui.Spacer(c)
		if m.Installed {
			label := "已安装"
			if m.Managed {
				label = "EnvMgr 已安装"
			}
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icCheck).Size(12, 12).TextColor(t.Success)
				ui.Text(c, label).FontSize(11).TextColor(t.TextMuted)
			})
			if a.iconButton(c, icFolder, "打开目录", false).Clicked() && m.Home != "" {
				openPath(m.Home)
			}
		} else if job := a.jobFor("manager:" + m.ID); job != nil && job.Running {
			ui.Text(c, job.Stage).FontSize(11).TextColor(t.TextMuted)
			if !job.Cancelling && ui.Button(c, "取消").Label("取消 "+job.Label).Clicked() {
				a.cancelJob(job)
			}
		} else if m.Install != nil {
			if ui.Button(c, "安装").Label("安装 " + m.Name).Clicked() {
				a.installManager(m)
			}
		} else {
			ui.Text(c, m.Hint).FontSize(10).TextColor(t.TextMuted).MaxWidth(220).SingleLine().Ellipsis("…")
			if a.iconButton(c, icExternal, "打开官网", false).Clicked() {
				openURL(m.Site)
			}
		}
	})
}

// ---- 下载安装 ----

func (a *App) downloadsPage(c *ui.Context) {
	t := c.Theme()
	kind := a.downloadsKind()
	// Whatever got the page here, the list on screen must belong to the tab
	// that is selected. Asking every frame makes the tab switch work without
	// depending on a widget reporting the change.
	a.ensureRemote(kind)

	ui.Column(c).Fill().Padding(20, 20, 22, 20).Gap(12).Children(func() {
		a.downloadJobs(c)
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Segmented(c, &a.downloadKind, kindNames()...).Label("SDK 类型")
			ui.Spacer(c)
			if ui.Switch(c, &a.filterLTS).Label("只看 LTS").Changed() {
				a.remoteList = ui.ListState{}
			}
			ui.Text(c, "仅 LTS").FontSize(11).TextColor(t.TextMuted)
			if a.button(c, icRefresh, "刷新").Clicked() {
				a.loadRemote(kind, true)
			}
		})

		switch {
		case a.remoteLoading && a.remoteKind == kind:
			ui.Column(c).Fill().Center().Gap(10).Children(func() {
				ui.Spinner(c).Size(22, 22)
				ui.Text(c, "正在读取 "+core.Info(kind).Name+" 的版本列表…").FontSize(12).TextColor(t.TextMuted)
			})
		case a.remoteErr != "" && a.remoteKind == kind:
			card := a.card(c).Padding(16).Gap(8)
			card.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icAlert).Size(14, 14).TextColor(t.Danger)
					ui.Text(c, "读取版本列表失败").FontSize(12.5).Bold()
				})
				ui.Text(c, a.remoteErr).FontSize(11).TextColor(t.TextMuted).Wrap()
				if ui.Button(c, "重试").Clicked() {
					a.loadRemote(kind, true)
				}
			})
		default:
			rows := a.groupsFor(kind)
			card := a.card(c).Fill().Gap(0).Padding(6, 6)
			card.Children(func() {
				a.remoteList.Key = func(i int) any { return rows[i].Version }
				ui.List(c, &a.remoteList, len(rows), func(i int) {
					a.remoteRow(c, rows[i])
				}).Grow(1).Children(func() {
					if len(rows) == 0 {
						ui.Column(c).Fill().Center().Gap(6).Children(func() {
							ui.Icon(c, icDownload).Size(20, 20).TextColor(t.TextMuted)
							ui.Text(c, "没有可显示的版本").FontSize(12).TextColor(t.TextMuted)
						})
					}
				})
			})
		}
	})
}

// remoteRow is one downloadable version. A version built by several vendors is
// one row with a chip per vendor; installing asks which build to download.
func (a *App) remoteRow(c *ui.Context, g remoteGroup) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).Padding(8, 10).Radius(7).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, g.Version).FontSize(12.5).Bold().Width(78).Shrink(0).SingleLine()
		if g.LTS {
			a.chip(c, "LTS", t.Success, t.Success.Alpha(0.12))
		}
		label := strings.TrimSpace(strings.TrimPrefix(g.Label, "LTS"))
		label = strings.TrimSpace(strings.TrimPrefix(label, "·"))
		if label != "" && label != "LTS" && !g.MultiVendor() {
			a.chip(c, label, t.TextMuted, t.SurfaceHover)
		}
		// Who builds this version. One chip per build, so the choice is
		// visible before the click.
		ui.Row(c).Gap(4).Children(func() {
			for _, opt := range g.Options {
				name := opt.VendorName
				if name == "" {
					name = "官方"
				}
				a.chip(c, shortVendor(name), t.Accent, t.Accent.Alpha(0.10))
			}
		})
		ui.Text(c, g.Note).FontSize(10.5).TextColor(t.TextMuted).Grow(1).SingleLine()
		if s := g.SizeText(); s != "" {
			ui.Text(c, s).FontSize(10.5).TextColor(t.TextMuted)
		}

		// A running install for any build of this version takes the row over.
		for _, opt := range g.Options {
			if job := a.jobFor(remoteJobID(opt)); job != nil && job.Running {
				ui.Column(c).Gap(3).Width(140).Shrink(0).Children(func() {
					ui.Progress(c, job.Progress()).Label("安装进度")
					ui.Text(c, job.Detail).FontSize(9.5).TextColor(t.TextMuted).SingleLine()
				})
				return
			}
		}
		if inst, ok := a.inventory.exact(g.Kind, g.Version); ok {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icCheck).Size(12, 12).TextColor(t.Success)
				ui.Text(c, "已安装 "+inst.Version).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			return
		}
		button := "安装"
		if g.MultiVendor() {
			button = "选择下载源"
		}
		if ui.Button(c, button).Label("安装 " + g.Version).Clicked() {
			if g.MultiVendor() {
				a.vendorPick = &vendorPick{Group: g}
			} else {
				a.startInstall(g.Options[0])
			}
		}
	})
}

// vendorPick is the dialog that asks which build of a version to download.
type vendorPick struct {
	Group remoteGroup
}

// vendorPicker draws the source chooser: one row per build of that version.
func (a *App) vendorPicker(c *ui.Context) {
	pick := a.vendorPick
	if pick == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		t := c.Theme()
		ui.Text(c, "选择 Java "+pick.Group.Version+" 的下载源").FontSize(14).Bold()
		ui.Text(c, "同一个版本由不同的厂商构建，补丁与许可证不同。").
			FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(420)
		ui.Column(c).Gap(6).Children(func() {
			for _, opt := range pick.Group.Options {
				opt := opt
				row := ui.Row(c).Gap(10).Padding(9, 10).Radius(8).AlignItems(ui.Center)
				row.Border(1, t.Border)
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				row.Children(func() {
					ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
						name := opt.VendorName
						if name == "" {
							name = "官方源"
						}
						ui.Text(c, name).FontSize(12.5).Bold().SingleLine()
						detail := opt.Label
						if detail == "" {
							detail = core.Info(opt.Kind).Name
						}
						if opt.Size > 0 {
							detail += " · " + core.HumanSize(opt.Size)
						}
						ui.Text(c, detail).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
					})
					if opt.LTS {
						a.chip(c, "LTS", t.Success, t.Success.Alpha(0.12))
					}
					if _, ok := a.inventory.exact(opt.Kind, opt.Version); ok {
						ui.Text(c, "已安装").FontSize(11).TextColor(t.TextMuted)
						return
					}
					if ui.Button(c, "下载").Label("下载 " + opt.VendorName).Clicked() {
						a.startInstall(opt)
						a.vendorPick = nil
					}
				})
			}
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				a.vendorPick = nil
			}
		})
	})
	if !open {
		a.vendorPick = nil
	}
}

// shortVendor trims a vendor name to the part that identifies it in a chip.
func shortVendor(name string) string {
	switch name {
	case "Microsoft Build of OpenJDK":
		return "Microsoft"
	case "Eclipse Temurin":
		return "Temurin"
	case "Amazon Corretto", "Azul Zulu Community", "BellSoft Liberica JDK":
		if i := strings.IndexByte(name, ' '); i > 0 {
			return name[:i]
		}
	}
	for _, prefix := range []string{"Eclipse ", "Amazon ", "Azul ", "BellSoft ", "Microsoft Build of "} {
		name = strings.TrimPrefix(name, prefix)
	}
	name = strings.Replace(name, " Community", "", 1)
	name = strings.Replace(name, " JDK", "", 1)
	return name
}

// ---- 体检 ----

// doctorPage lists every configuration item EnvMgr depends on, with the ones
// that need attention first. The checks themselves live in core.Diagnose, so
// the window and `envmgr doctor` report the same thing.
func (a *App) doctorPage(c *ui.Context) {
	t := c.Theme()
	rep := a.report
	sections := a.doctor.Sections
	rowCount := 0
	for _, sec := range sections {
		if len(sec.Checks) > 0 {
			rowCount += 1 + len(sec.Checks)
		}
	}
	// Locate a row across the small, fixed number of report sections without
	// walking or copying the checks that are outside the viewport.
	locate := func(row int) (int, int) {
		for i, sec := range sections {
			if len(sec.Checks) == 0 {
				continue
			}
			if row <= len(sec.Checks) {
				return i, row - 1 // -1 identifies the section heading.
			}
			row -= 1 + len(sec.Checks)
		}
		return -1, -1
	}
	a.doctorList.Key = func(row int) any {
		section, check := locate(row)
		return fmt.Sprintf("doctor:%d:%d", section, check)
	}
	ui.Column(c).Fill().Padding(20, 20, 24, 20).Gap(14).Children(func() {

		// Verdict banner: one line the user can read at a glance.
		hero := a.card(c).Gap(8)
		hero.Background(t.Surface)
		hero.Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				fg := t.Success
				icon := icCheck
				if rep.Fail > 0 {
					fg, icon = t.Danger, icAlert
				} else if rep.Warn > 0 {
					fg, icon = t.Warning, icAlert
				}
				ui.Box(c).Size(38, 38).Radius(10).Background(fg.Alpha(0.12)).Center().Children(func() {
					ui.Icon(c, icon).Size(19, 19).TextColor(fg)
				})
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, rep.Summary()).FontSize(14).Bold()
					ui.Text(c, "逐项检查运行环境、存储、终端钩子、工具链、项目与设置").
						FontSize(11).TextColor(t.TextMuted).SingleLine()
				})
				ui.Row(c).Gap(6).Children(func() {
					a.countChip(c, "正常", rep.OK, t.Success)
					a.countChip(c, "注意", rep.Warn, t.Warning)
					a.countChip(c, "有问题", rep.Fail, t.Danger)
					if rep.Info > 0 {
						a.countChip(c, "信息", rep.Info, t.TextMuted)
					}
				})
			})
		})

		// The cached report is already ordered with problems first. Only the
		// section headings and checks in view build UI nodes.
		a.card(c).Fill().Gap(0).Padding(12, 16).Children(func() {
			ui.List(c, &a.doctorList, rowCount, func(row int) {
				section, check := locate(row)
				if section < 0 {
					return
				}
				sec := sections[section]
				if check >= 0 {
					a.checkRow(c, sec.Checks[check])
					return
				}
				ui.Row(c).Gap(8).Padding(10, 0, 8, 0).AlignItems(ui.Center).Children(func() {
					ui.Text(c, sec.Title).FontSize(13).Bold()
					ui.Text(c, sec.Desc).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
					ui.Spacer(c)
					if n := a.doctor.WorstCounts[section]; n > 0 {
						a.chip(c, fmt.Sprintf("%d 项待处理", n), t.Warning, t.Warning.Alpha(0.12))
					}
				})
			}).Grow(1)
		})
	})
}

// countChip is one tally in the verdict banner.
func (a *App) countChip(c *ui.Context, label string, n int, fg ui.Color) {
	t := c.Theme()
	if n == 0 {
		a.chip(c, fmt.Sprintf("%s 0", label), t.TextMuted, t.SurfaceHover)
		return
	}
	a.chip(c, fmt.Sprintf("%s %d", label, n), fg, fg.Alpha(0.12))
}

// checkRow draws one inspected item, with its fix button when EnvMgr can act.
func (a *App) checkRow(c *ui.Context, ck core.Check) {
	t := c.Theme()
	fg, icon := t.Success, icCheck
	switch ck.Level {
	case core.LevelWarn:
		fg, icon = t.Warning, icAlert
	case core.LevelFail:
		fg, icon = t.Danger, icAlert
	case core.LevelInfo:
		fg, icon = t.TextMuted, icInfo
	}
	ui.Row(c).Padding(7, 0).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(18, 18).Shrink(0).Center().Children(func() {
			ui.Icon(c, icon).Size(13, 13).TextColor(fg)
		})
		ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, ck.Title).FontSize(12).Shrink(0)
				ui.Text(c, ck.Detail).FontSize(11).TextColor(t.TextMuted).
					SingleLine().Ellipsis("…")
			})
			if ck.Hint != "" && ck.Level != core.LevelOK {
				ui.Text(c, ck.Hint).FontSize(10.5).TextColor(fg.Alpha(0.9)).
					Wrap().MaxWidth(560)
			}
		})
		if ck.Fix != core.FixNone {
			a.fixButton(c, ck)
		}
	})
}

// fixButton offers the action that resolves a check.
func (a *App) fixButton(c *ui.Context, ck core.Check) {
	switch ck.Fix {
	case core.FixInstallHook:
		if ui.Button(c, "安装钩子").Label("安装终端钩子").Clicked() {
			targets, err := core.InstallHook(core.Shell(ck.FixArg))
			if err != nil {
				a.pushToast(err.Error())
			} else {
				a.pushToast("已写入 " + targets + "，新开终端生效")
				a.requestServiceUpdate()
			}
		}
	case core.FixRefresh:
		if ui.Button(c, "重新扫描").Label("重新扫描 SDK").Clicked() {
			a.refreshAsync()
		}
	case core.FixOpenConfigDir:
		if ui.Button(c, "打开").Label("打开配置目录").Clicked() {
			if dir, err := core.ConfigDir(); err == nil {
				openPath(dir)
			}
		}
	case core.FixOpenDataDir:
		if ui.Button(c, "打开").Label("打开 SDK 目录").Clicked() {
			if dir, err := core.DataDirRoot(a.svc.Config()); err == nil {
				openPath(dir)
			}
		}
	}
}

// worstCount is how many checks in a section need attention.
func worstCount(sec core.Section) int {
	n := 0
	for _, ck := range sec.Checks {
		if ck.Level == core.LevelWarn || ck.Level == core.LevelFail {
			n++
		}
	}
	return n
}

// sortedChecks puts failures first, then warnings, then the rest.
func sortedChecks(in []core.Check) []core.Check {
	out := make([]core.Check, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		return rank(out[i].Level) < rank(out[j].Level)
	})
	return out
}

func rank(l core.Level) int {
	switch l {
	case core.LevelFail:
		return 0
	case core.LevelWarn:
		return 1
	case core.LevelOK:
		return 2
	}
	return 3
}

// sortedSections moves sections with problems to the top.
func sortedSections(in []core.Section) []core.Section {
	out := make([]core.Section, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		return sectionRank(out[i]) < sectionRank(out[j])
	})
	return out
}

// sectionRank puts sections that need work first: failures, then warnings,
// then sections that are only informational. Lower sorts earlier.
func sectionRank(s core.Section) int {
	rank := 2
	for _, ck := range s.Checks {
		switch ck.Level {
		case core.LevelFail:
			return 0
		case core.LevelWarn:
			rank = 1
		}
	}
	return rank
}

// ---- 设置 ----

func (a *App) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Fill().Padding(20, 20, 24, 20).Gap(14).Children(func() {

		// Appearance
		appearance := a.card(c).Gap(10)
		appearance.Children(func() {
			ui.Text(c, "外观").FontSize(13).Bold()
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "主题").FontSize(12).Width(120).Shrink(0)
				idx := themeIndex(a.themeMode)
				if ui.Segmented(c, &idx, "跟随系统", "浅色", "深色").Label("主题").Changed() {
					a.themeMode = []string{"system", "light", "dark"}[idx]
					a.saveSettings()
				}
			})
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "语言").FontSize(12).Width(120).Shrink(0)
				ui.Text(c, "中文（后续加入英文）").FontSize(11).TextColor(t.TextMuted)
			})
		})

		// Behavior
		behavior := a.card(c).Gap(12)
		behavior.Children(func() {
			ui.Text(c, "行为").FontSize(13).Bold()
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, "跟随项目自带的版本文件").FontSize(12)
					ui.Text(c, "读取 .nvmrc、.python-version、.java-version、go.mod、rust-toolchain、.tool-versions").
						FontSize(10.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
				})
				if ui.Switch(c, &a.followMark).Label("跟随项目自带的版本文件").Changed() {
					a.saveSettings()
					a.recompute()
				}
			})
		})

		// Global defaults: the version a directory gets when it says nothing.
		// Without them EnvMgr only does anything inside projects, and a user
		// who fixes Java once still meets the system JDK in a scratch folder.
		defaultsCard := a.card(c).Gap(10)
		defaultsCard.Children(func() {
			ui.Text(c, "全局默认版本").FontSize(13).Bold()
			ui.Text(c, "项目里没有声明版本时使用这里的设置；项目自己写了版本，仍然以项目为准。").
				FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(560)
			anyChoice := false
			for _, kind := range core.Kinds {
				list := core.InstalledOf(a.installed, kind)
				if len(list) == 0 {
					continue
				}
				anyChoice = true
				kind := kind
				labels := []string{"不设置"}
				versions := map[string]string{}
				for _, inst := range list {
					label := inst.Version
					if inst.Manager != "" && inst.Manager != "envmgr" {
						label += " · " + inst.Manager
					}
					if _, dup := versions[label]; dup {
						continue
					}
					versions[label] = inst.Version
					labels = append(labels, label)
				}
				choice := "不设置"
				if v := a.defaults[kind]; v != "" {
					for label, version := range versions {
						if version == v {
							choice = label
							break
						}
					}
				}
				info := core.Info(kind)
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, info.Name).FontSize(12).Width(120).Shrink(0)
					if ui.Select(c, &choice, labels).Label(info.Name + " 默认版本").Changed() {
						a.setDefault(kind, versions[choice])
					}
				})
			}
			if !anyChoice {
				ui.Text(c, "本机还没有可选的版本，先在「下载安装」里装一个。").
					FontSize(10.5).TextColor(t.TextMuted)
			}
		})

		// Terminal integration: the hook is what makes the user's own
		// terminals switch versions, not just the one EnvMgr opens.
		integ := a.card(c).Gap(10)
		integ.Children(func() {
			ui.Text(c, "终端集成").FontSize(13).Bold()
			sh := a.settings.Shell
			files := a.settings.HookFiles
			installed := a.settings.HookInstalled
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, "在任何终端里自动切换").FontSize(12)
					ui.Text(c, "装一次钩子后，你自己开的终端进入项目目录就会切到项目锁定的版本，离开自动还原。").
						FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(520)
					for _, f := range files {
						mark := "·"
						if a.settings.HookInstalledIn[f] {
							mark = "✓"
						}
						ui.Text(c, mark+" "+f).FontSize(10).TextColor(t.TextMuted).
							SingleLine().Ellipsis("…")
					}
				})
				if installed {
					a.chip(c, "已安装", t.Success, t.SurfaceHover)
					if ui.Button(c, "移除").Clicked() {
						if _, err := core.UninstallHook(sh); err != nil {
							a.pushToast(err.Error())
						} else {
							a.pushToast("已移除钩子，新开的终端不再自动切换")
							a.requestServiceUpdate()
						}
					}
				} else if ui.PrimaryButton(c, "安装到 shell").Clicked() {
					targets, err := core.InstallHook(sh)
					if err != nil {
						a.pushToast(err.Error())
					} else {
						a.pushToast("已写入 " + targets + "，新开终端生效")
						a.requestServiceUpdate()
					}
				}
			})
			ui.Text(c, "只往你的 shell 启动文件里追加一段带标记的内容，原文件自动备份；不装也不影响 EnvMgr 打开的终端。").
				FontSize(10).TextColor(t.TextMuted).Wrap().MaxWidth(560)
		})

		// Git identities: one binding per remote address, so the same person can
		// commit as different identities to different servers.
		gitCard := a.card(c).Gap(10)
		gitCard.Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Git 提交身份").FontSize(13).Bold()
				ui.Spacer(c)
				if a.settings.GitRulesInstalled {
					a.chip(c, "自动套用中", t.Success, t.Success.Alpha(0.12))
				} else {
					a.chip(c, "未启用自动套用", t.TextMuted, t.SurfaceHover)
				}
			})
			ui.Text(c, "一个项目可保存多个远端地址，每个地址可绑定多套用户名和邮箱。在项目页选择地址后，应用需要的身份。").
				FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(560)

			profiles := a.settings.Profiles
			if len(profiles) == 0 {
				ui.Text(c, "还没有绑定。在项目页的 Git 卡里，按当前仓库的远端地址新建一条即可。").
					FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(560)
			} else {
				for _, prof := range profiles {
					prof := prof
					ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(8, 10).Radius(8).
						Background(t.SurfaceHover).Children(func() {
						ui.Icon(c, icBranch).Size(13, 13).TextColor(t.TextMuted)
						ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
							// The name first, because that is what the user
							// recognises; the address stays visible so the
							// binding is still identifiable.
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, prof.Display()).FontSize(11.5).Bold().
									SingleLine().Ellipsis("…")
								// When the binding has a name, show the part of the
								// address that tells two of them apart: the owner
								// or repository, not the host they share.
								if scope := core.ProfileScope(prof.Key); prof.Label != "" && scope != "" {
									a.chip(c, scope, t.TextMuted, t.SurfaceHover)
								}
							})
							if prof.Label == "" {
								ui.Text(c, prof.Key).FontSize(10).TextColor(t.TextMuted).
									Font("monospace").SingleLine().Ellipsis("…")
							}
							ui.Text(c, prof.Name+" <"+prof.Email+">").FontSize(10.5).
								TextColor(t.TextMuted).SingleLine().Ellipsis("…")
						})
						if ui.Button(c, "删除").Label("删除这条绑定").Clicked() {
							a.deleteGitProfile(prof.IdentityID())
						}
					})
				}
			}

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if a.settings.GitRulesInstalled {
					if ui.Button(c, "停止自动套用").Clicked() {
						a.toggleGitRules(false)
					}
				} else if len(profiles) > 0 {
					if ui.PrimaryButton(c, "让 git 自动套用").Clicked() {
						a.toggleGitRules(true)
					}
				}
			})
			if a.settings.GitRulesInstalled {
				ui.Text(c, "规则写在全局 git 配置里的一段标记块中（原文件已备份为 .envmgr.bak），只增加 includeIf，不改你的 user.name。").
					FontSize(10).TextColor(t.TextMuted).Wrap().MaxWidth(560)
			}
			if a.settings.Ready && !a.settings.GitRemoteRulesSupported {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icAlert).Size(12, 12).TextColor(t.Warning)
					ui.Text(c, "当前 git 版本低于 2.36，不支持按远端地址自动套用；绑定仍可手动写入单个仓库。").
						FontSize(10.5).TextColor(t.Warning).Wrap().MaxWidth(560)
				})
			}
		})

		// Storage
		storage := a.card(c).Gap(10)
		storage.Children(func() {
			ui.Text(c, "存储").FontSize(13).Bold()
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "SDK 安装目录").FontSize(12)
				ui.TextInput(c, &a.dataDir).Label("SDK 安装目录").
					Placeholder(defaultDataDirHint())
				ui.Text(c, "留空则使用 ~/.envmgr/sdk。EnvMgr 只会写这里，不动系统 PATH。").
					FontSize(10.5).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, "保存").Clicked() {
					a.saveSettings()
					c.Toast("设置已保存")
				}
				if ui.Button(c, "打开数据目录").Clicked() {
					if root, err := core.DataDirRoot(a.svc.Config()); err == nil {
						openPath(root)
					}
				}
			})
		})

		// Network: where downloads come from and how they get there.
		a.networkCard(c)

		// Shell integration
		sh := a.card(c).Gap(10)
		sh.Children(func() {
			ui.Text(c, "终端集成").FontSize(13).Bold()
			ui.Text(c, "把下面这段加到 shell 启动文件里，进入项目目录时自动切换版本。EnvMgr 不会自己修改任何配置文件。").
				FontSize(11).TextColor(t.TextMuted).Wrap().MaxWidth(680)
			shell := a.settings.Shell
			script := a.settings.HookScript
			box := ui.Column(c).Padding(10).Radius(8).Background(t.Background).Border(1, t.Border)
			box.Children(func() {
				ui.Text(c, script).FontSize(10.5).Font("monospace").TextColor(t.TextMuted).MaxLines(8)
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "复制脚本").Clicked() {
					c.WriteClipboard(script)
					c.Toast("已复制，粘贴到 " + core.HookFile(shell))
				}
				if ui.Button(c, "显示启动文件").Clicked() {
					mygoRevealPath(core.HookFile(shell))
				}
				ui.Text(c, core.HookFile(shell)).FontSize(10.5).TextColor(t.TextMuted).
					Grow(1).SingleLine().Ellipsis("…")
			})
		})

		// About
		about := a.card(c).Gap(6)
		about.Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(30, 30).Radius(8).Background(t.Accent).Center().Children(func() {
					ui.Icon(c, icLogo).Size(18, 18).TextColor(t.AccentText)
				})
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, "EnvMgr").FontSize(13).Bold()
					ui.Text(c, "项目 SDK 环境管理器 · 纯 Go、无 cgo、无 webview").FontSize(10.5).TextColor(t.TextMuted)
				})
				ui.Text(c, "v1.0.0").FontSize(11).TextColor(t.TextMuted)
			})
		})
	})
}

// themeIndex maps the stored mode to the segmented control.
func themeIndex(mode string) int {
	switch mode {
	case "light":
		return 1
	case "dark":
		return 2
	}
	return 0
}

// kindNames lists the segmented labels for the SDK kinds.
func kindNames() []string {
	names := make([]string, len(core.Kinds))
	for i, k := range core.Kinds {
		names[i] = core.Info(k).Name
	}
	return names
}

// findResolution looks one kind up in a project's resolutions.
func findResolution(list []core.Resolution, kind core.Kind) (core.Resolution, bool) {
	for _, r := range list {
		if r.Kind == kind {
			return r, true
		}
	}
	return core.Resolution{}, false
}

// versionOptions builds the picker's labels and the pin each one sets.
func versionOptions(kind core.Kind, r core.Resolution, installed []core.Installed) (labels, values []string) {
	follow := "跟随项目文件"
	if r.Source == "" || r.Source == "pin" {
		follow = "不固定（跟随项目文件）"
	}
	labels = append(labels, follow)
	values = append(values, "")
	for _, inst := range installed {
		labels = append(labels, inst.Version)
		values = append(values, inst.Version)
	}
	return labels, values
}

// shortPath shortens a path for a narrow row.
func shortPath(p string) string {
	if len(p) <= 42 {
		return p
	}
	return "…" + p[len(p)-41:]
}

// kindSummary describes the versions a project resolved, for tooltips.
func kindSummary(res []core.Resolution) string {
	var parts []string
	for _, r := range res {
		v := r.Expr
		if r.Installed != nil {
			v = r.Installed.Version
		}
		parts = append(parts, strings.TrimSpace(core.Info(r.Kind).Name+" "+v))
	}
	return strings.Join(parts, " · ")
}
