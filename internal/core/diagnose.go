package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Level is how a single check came out.
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelFail
	LevelInfo
)

// Label is the short word shown next to a check.
func (l Level) Label() string {
	switch l {
	case LevelOK:
		return "正常"
	case LevelWarn:
		return "注意"
	case LevelFail:
		return "有问题"
	}
	return "信息"
}

// Fix is something EnvMgr can do about a check, so the window can offer a
// button instead of only printing advice.
type Fix int

const (
	FixNone Fix = iota
	FixInstallHook
	FixRefresh
	FixOpenConfigDir
	FixOpenDataDir
)

// Check is one inspected configuration item.
type Check struct {
	Title  string // what was inspected
	Detail string // what was found
	Level  Level
	Hint   string // what to do about it, when it is not OK
	Fix    Fix
	// FixArg carries the shell for the hook fix.
	FixArg string
}

// Section groups checks that belong to one subject.
type Section struct {
	Title  string
	Desc   string
	Checks []Check
}

// Report is the whole doctor run.
type Report struct {
	Sections []Section
	OK       int
	Warn     int
	Fail     int
	// Info counts checks that state a fact rather than judge it, so the four
	// tallies always add up to Total.
	Info  int
	Total int
}

// Healthy reports whether nothing failed.
func (r Report) Healthy() bool { return r.Fail == 0 }

// Summary is a one line verdict for the dashboard and the command line.
func (r Report) Summary() string {
	switch {
	case r.Fail > 0:
		return fmt.Sprintf("%d 项检查有问题，%d 项需要注意", r.Fail, r.Warn)
	case r.Warn > 0:
		return fmt.Sprintf("检查通过，%d 项需要注意", r.Warn)
	}
	return fmt.Sprintf("%d 项配置检查通过", r.Total)
}

// Section indexes, named so the checks read clearly.
const (
	secRuntime = iota
	secStorage
	secTerminal
	secToolchains
	secProjects
	secSettings
)

// putFunc records one check in a section.
type putFunc func(section int, c Check)

// Diagnose inspects the configuration EnvMgr depends on and reports every item
// it can judge. It changes nothing: the window turns the Fix fields into
// buttons, so the user stays in control.
func Diagnose(svc *Service) Report {
	return DiagnoseWithInstalled(svc, svc.Installed())
}

// DiagnoseWithInstalled checks the supplied discovery snapshot without starting
// another SDK scan. Background UI updates share it with their project resolutions.
func DiagnoseWithInstalled(svc *Service, installed []Installed) Report {
	rep := Report{Sections: []Section{
		{Title: "运行环境", Desc: "EnvMgr 自身与它依赖的运行时"},
		{Title: "数据与存储", Desc: "状态文件、SDK 目录、下载缓存"},
		{Title: "终端集成", Desc: "进入项目能否自动切换，取决于这里的每一项"},
		{Title: "工具链", Desc: "这台机器上 EnvMgr 能看到哪些版本"},
		{Title: "项目", Desc: "每个项目的版本声明能否落地"},
		{Title: "设置与环境变量", Desc: "影响解析结果的可选项与外部变量"},
	}}
	put := func(section int, c Check) {
		rep.Sections[section].Checks = append(rep.Sections[section].Checks, c)
		rep.Total++
		switch c.Level {
		case LevelOK:
			rep.OK++
		case LevelWarn:
			rep.Warn++
		case LevelFail:
			rep.Fail++
		default:
			rep.Info++
		}
	}
	checkRuntime(put)
	checkStorage(svc, put)
	checkTerminal(put)
	checkToolchains(installed, put)
	checkProjects(svc, installed, put)
	checkSettings(svc, put)
	return rep
}

func checkRuntime(put putFunc) {
	put(secRuntime, Check{
		Title:  "Go 运行时",
		Detail: fmt.Sprintf("%s %s/%s，无 cgo 的静态二进制", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		Level:  LevelOK,
	})
	exe, err := os.Executable()
	if err != nil {
		put(secRuntime, Check{
			Title:  "可执行文件位置",
			Detail: err.Error(),
			Level:  LevelFail,
			Hint:   "EnvMgr 无法确定自身路径，终端钩子会因此调不到程序。",
		})
		return
	}
	put(secRuntime, Check{Title: "可执行文件位置", Detail: exe, Level: LevelOK})
}

func checkStorage(svc *Service, put putFunc) {
	cfg := svc.Config()

	cfgDir, cfgErr := ConfigDir()
	if cfgErr != nil {
		put(secStorage, Check{Title: "配置目录", Detail: cfgErr.Error(), Level: LevelFail})
	} else {
		put(secStorage, writableCheck("配置目录", cfgDir, FixOpenConfigDir))
	}

	if dataDir, err := DataDirRoot(cfg); err == nil {
		put(secStorage, writableCheck("SDK 安装目录", dataDir, FixOpenDataDir))
	} else {
		put(secStorage, Check{Title: "SDK 安装目录", Detail: err.Error(), Level: LevelFail})
	}

	if cfgErr == nil {
		path := filepath.Join(cfgDir, "state.json")
		st, err := os.Stat(path)
		switch {
		case os.IsNotExist(err):
			put(secStorage, Check{
				Title:  "状态文件",
				Detail: path + "（还没有内容，首次保存时创建）",
				Level:  LevelInfo,
			})
		case err != nil:
			put(secStorage, Check{Title: "状态文件", Detail: path + ": " + err.Error(), Level: LevelFail})
		default:
			put(secStorage, Check{
				Title:  "状态文件",
				Detail: fmt.Sprintf("%s（%s）", path, HumanSize(st.Size())),
				Level:  LevelOK,
			})
		}

		cache := filepath.Join(cfgDir, "downloads")
		if size := DirSize(cache); size > 0 {
			put(secStorage, Check{
				Title:  "下载缓存",
				Detail: fmt.Sprintf("%s（%s）", cache, HumanSize(size)),
				Level:  LevelInfo,
				Hint:   "装好的版本不依赖缓存，可以随时清空。",
			})
		} else {
			put(secStorage, Check{Title: "下载缓存", Detail: cache + "（空）", Level: LevelInfo})
		}
	}
}

// writableCheck verifies a directory can be created and written, which is what
// every install path needs.
func writableCheck(title, dir string, fix Fix) Check {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Check{
			Title:  title,
			Detail: dir + "：" + err.Error(),
			Level:  LevelFail,
			Hint:   "目录建不出来，安装和状态保存都会失败。",
			Fix:    fix,
		}
	}
	probe := filepath.Join(dir, ".envmgr-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return Check{
			Title:  title,
			Detail: dir + "：不可写（" + err.Error() + "）",
			Level:  LevelFail,
			Hint:   "换一个有写权限的目录，或在设置里改到别处。",
			Fix:    fix,
		}
	}
	_ = os.Remove(probe)
	return Check{Title: title, Detail: dir + "（可写）", Level: LevelOK, Fix: fix}
}

func checkTerminal(put putFunc) {
	sh := CurrentShell()
	put(secTerminal, Check{Title: "当前 shell", Detail: string(sh), Level: LevelOK})

	installedAny := false
	for _, file := range HookFiles(sh) {
		title := "钩子 · " + shortPath(file)
		info, err := os.Stat(file)
		if os.IsNotExist(err) {
			put(secTerminal, Check{
				Title:  title,
				Detail: "启动文件不存在",
				Level:  LevelInfo,
				Hint:   "这个 shell 还没用过；装钩子时会一并创建。",
				Fix:    FixInstallHook,
				FixArg: string(sh),
			})
			continue
		}
		if err != nil {
			put(secTerminal, Check{Title: title, Detail: err.Error(), Level: LevelFail})
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			put(secTerminal, Check{Title: title, Detail: err.Error(), Level: LevelFail})
			continue
		}
		text := string(raw)
		if !strings.Contains(text, hookBegin) {
			put(secTerminal, Check{
				Title:  title,
				Detail: fmt.Sprintf("未安装（文件 %s）", HumanSize(info.Size())),
				Level:  LevelWarn,
				Hint:   "没有钩子时，只有 EnvMgr 打开的终端会切换版本。",
				Fix:    FixInstallHook,
				FixArg: string(sh),
			})
			continue
		}
		installedAny = true
		level, detail, hint := LevelOK, "已安装", ""
		// Windows PowerShell decodes .ps1 as ANSI without a BOM, which breaks
		// the block silently: the shell works, the versions never switch.
		if strings.HasSuffix(file, ".ps1") && runtime.GOOS == "windows" {
			if len(raw) < 3 || raw[0] != 0xEF || raw[1] != 0xBB || raw[2] != 0xBF {
				level = LevelFail
				detail = "已安装，但文件没有 UTF-8 BOM"
				hint = "PowerShell 会按 ANSI 解码，钩子静默失效。重装一次即可修正。"
			}
		}
		// The hook calls EnvMgr by absolute path. That path goes stale when the
		// program is moved or deleted, which breaks switching with no other
		// symptom — so check the path the hook actually records.
		if hookPath, ok := hookProgramPath(text); ok {
			if _, err := os.Stat(hookPath); err != nil {
				if level == LevelOK {
					level = LevelWarn
				}
				detail += "，但钩子调用的程序已不存在"
				hint = "钩子里写的是 " + hookPath + "，它已经不在那里了。重装一次钩子即可指向当前位置。"
			}
		}
		put(secTerminal, Check{
			Title:  title,
			Detail: detail,
			Level:  level,
			Hint:   hint,
			Fix:    FixInstallHook,
			FixArg: string(sh),
		})
		if _, err := os.Stat(file + ".envmgr.bak"); err == nil {
			put(secTerminal, Check{
				Title:  "备份 · " + shortPath(file),
				Detail: file + ".envmgr.bak",
				Level:  LevelInfo,
				Hint:   "安装钩子前你的原文件，可随时手工还原。",
			})
		}
	}

	if installedAny {
		put(secTerminal, Check{
			Title:  "自己开的终端",
			Detail: "进入项目自动切换，离开自动还原",
			Level:  LevelOK,
		})
	} else {
		put(secTerminal, Check{
			Title:  "自己开的终端",
			Detail: "不会切换，只有 EnvMgr 打开的终端会",
			Level:  LevelWarn,
			Hint:   "装一次钩子就能让任何终端生效。",
			Fix:    FixInstallHook,
			FixArg: string(sh),
		})
	}

	if v := os.Getenv("ENVMGR_NO_BANNER"); v != "" {
		put(secTerminal, Check{
			Title:  "进入提示",
			Detail: "已关闭（ENVMGR_NO_BANNER=" + v + "）",
			Level:  LevelInfo,
			Hint:   "删掉这个环境变量就恢复彩色提示。",
		})
	} else {
		put(secTerminal, Check{
			Title:  "进入提示",
			Detail: "开启，进入项目时打印彩色一行",
			Level:  LevelOK,
		})
	}

	if term, ok := detectTerminal(); ok {
		put(secTerminal, Check{Title: "可用终端", Detail: term, Level: LevelOK})
	} else {
		put(secTerminal, Check{
			Title:  "可用终端",
			Detail: "没有找到已知终端程序",
			Level:  LevelInfo,
			Hint:   "窗口里的「打开终端」会退回系统默认。",
		})
	}
}

// detectTerminal reports the program the terminal button would use.
func detectTerminal() (string, bool) {
	for _, name := range []string{
		"wt.exe", "pwsh", "powershell", "cmd",
		"gnome-terminal", "konsole", "xfce4-terminal", "alacritty", "kitty", "xterm",
	} {
		if p, err := exec.LookPath(name); err == nil {
			return name + " → " + p, true
		}
	}
	return "", false
}

func checkToolchains(installed []Installed, put putFunc) {
	if len(installed) == 0 {
		put(secToolchains, Check{
			Title:  "已发现版本",
			Detail: "一个都没有",
			Level:  LevelWarn,
			Hint:   "去「下载安装」装一个，或在「工具链」里手动添加已装好的目录。",
			Fix:    FixRefresh,
		})
		return
	}
	byKind := map[Kind][]Installed{}
	for _, in := range installed {
		byKind[in.Kind] = append(byKind[in.Kind], in)
	}
	put(secToolchains, Check{
		Title:  "已发现版本",
		Detail: fmt.Sprintf("共 %d 个，覆盖 %d 种 SDK", len(installed), len(byKind)),
		Level:  LevelOK,
		Fix:    FixRefresh,
	})
	for _, k := range Kinds {
		list := byKind[k]
		name := Info(k).Name
		if len(list) == 0 {
			put(secToolchains, Check{
				Title:  name,
				Detail: "没有可用的版本",
				Level:  LevelInfo,
				Hint:   "需要时在「下载安装」里装，或在「工具链」里手动添加。",
			})
			continue
		}
		versions := make([]string, 0, len(list))
		for _, in := range list {
			versions = append(versions, in.Version)
		}
		put(secToolchains, Check{
			Title:  name,
			Detail: fmt.Sprintf("%d 个：%s", len(list), strings.Join(versions, "、")),
			Level:  LevelOK,
		})
	}
}

func checkProjects(svc *Service, installed []Installed, put putFunc) {
	if manual := svc.ManualInstalls(); len(manual) > 0 {
		put(secProjects, Check{
			Title:  "手动添加的版本",
			Detail: fmt.Sprintf("%d 个（只登记，不复制也不删除你的文件）", len(manual)),
			Level:  LevelOK,
		})
	}
	projects := svc.Projects()
	if len(projects) == 0 {
		put(secProjects, Check{
			Title:  "项目",
			Detail: "还没有项目",
			Level:  LevelInfo,
			Hint:   "在「项目」页添加一个目录，就能为它固定 SDK 版本。",
		})
		return
	}
	follow := svc.Config().FollowMarkers
	for _, p := range projects {
		res := Resolve(p, installed, follow)
		if len(res) == 0 {
			put(secProjects, Check{
				Title:  p.Name,
				Detail: p.Path + "：没有版本声明",
				Level:  LevelInfo,
				Hint:   "在「项目」页为它选版本，或放一个 .nvmrc / go.mod 之类的文件。",
			})
			continue
		}
		var okParts, missingParts []string
		for _, r := range res {
			if r.Err != nil {
				missingParts = append(missingParts, "配置错误："+r.Err.Error())
				continue
			}
			name := Info(r.Kind).Name
			if r.Missing {
				missingParts = append(missingParts, name+" "+r.Expr)
				continue
			}
			okParts = append(okParts, name+" "+r.Installed.Version)
		}
		if len(missingParts) > 0 {
			put(secProjects, Check{
				Title:  p.Name,
				Detail: p.Path + "：缺 " + strings.Join(missingParts, "、"),
				Level:  LevelFail,
				Hint:   "这个版本没装，进入目录不会切换。",
			})
			continue
		}
		put(secProjects, Check{
			Title:  p.Name,
			Detail: p.Path + "：" + strings.Join(okParts, "、"),
			Level:  LevelOK,
		})
	}
}

func checkSettings(svc *Service, put putFunc) {
	cfg := svc.Config()

	follow := "关闭：只认 EnvMgr 里保存的版本"
	if cfg.FollowMarkers {
		follow = "开启：也读 .nvmrc、.python-version、go.mod 等文件"
	}
	put(secSettings, Check{Title: "跟随项目版本文件", Detail: follow, Level: LevelOK})

	if cfg.DataDir != "" {
		put(secSettings, Check{Title: "自定义 SDK 目录", Detail: cfg.DataDir, Level: LevelOK})
	} else {
		put(secSettings, Check{Title: "SDK 目录", Detail: "默认的 ~/.envmgr/sdk", Level: LevelOK})
	}

	proxy := ProxyLabel(cfg.Proxy)
	if cfg.Proxy != "" && cfg.Proxy != ProxyDirect {
		proxy += " " + cfg.Proxy
	}
	put(secSettings, Check{Title: "下载代理", Detail: proxy, Level: LevelOK})

	for _, m := range cfg.MirrorSummaries() {
		where := m.Base
		switch {
		case where == "":
			where = m.Default + "（官方）"
		case m.Preset != "":
			where += "（" + m.Preset + " 镜像）"
		default:
			where += "（自定义）"
		}
		put(secSettings, Check{Title: Info(m.Kind).Name + " 下载源", Detail: where, Level: LevelOK})
	}

	// A stale global variable is a real trap: it points at a directory that no
	// longer exists, and every tool started outside a project inherits the
	// breakage. EnvMgr does not touch global state, so it reports instead.
	for _, name := range []string{"GOROOT", "JAVA_HOME", "GOPATH", "NVM_HOME", "PYENV_ROOT", "JENV_ROOT"} {
		val, ok := os.LookupEnv(name)
		if !ok || val == "" {
			continue
		}
		if _, err := os.Stat(val); err != nil {
			put(secSettings, Check{
				Title:  name,
				Detail: val + "（目录不存在）",
				Level:  LevelWarn,
				Hint:   "这是系统环境变量，EnvMgr 不会改它。它指向的目录已经没了，项目外的工具可能因此报错。",
			})
			continue
		}
		put(secSettings, Check{Title: name, Detail: val, Level: LevelOK})
	}
}

// hookProgramPath returns the program path the hook script calls.
//
// It looks only at lines that ask EnvMgr for an environment: the word "envmgr"
// also appears in the block's comments, and matching those grabs wrong text.
func hookProgramPath(script string) (string, bool) {
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, "env --shell") {
			continue
		}
		if m := hookPathRe.FindStringSubmatch(line); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// hookPathRe matches the quoted program path in a line that calls EnvMgr.
// The name is not fixed: the same template is used by test and snapshot
// binaries, and a user may rename the executable.
var hookPathRe = regexp.MustCompile(`['"]([^'"]+)['"]`)

// shortPath keeps the last two elements, so a check line stays readable.
func shortPath(p string) string {
	dir, file := filepath.Split(p)
	return filepath.Join(filepath.Base(filepath.Clean(dir)), file)
}
