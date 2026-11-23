// Package cli is the command-line half of EnvMgr. It shares the core with the
// window, so a shell gets exactly the environment the app shows.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"envmgr/internal/core"
)

// commands is the set of subcommands, in the order help lists them.
var commands = []struct {
	Name string
	Desc string
}{
	{"env", "打印当前目录的激活脚本，供 shell eval"},
	{"run", "在项目环境中运行一条命令"},
	{"use", "在项目里固定版本，写入 .envmgr.json"},
	{"default", "设置项目没写版本时使用的全局默认版本"},
	{"list", "列出已安装的版本，或可下载的版本"},
	{"outdated", "对比已装版本与各自系列的最新版、维护状态"},
	{"upgrade", "把 EnvMgr 装的版本升级到同系列最新"},
	{"install", "从官方源下载并安装一个版本"},
	{"remove", "删除 EnvMgr 安装的版本"},
	{"doctor", "检查项目要的版本是否都在本机"},
	{"hook", "打印可选安装的 shell 钩子"},
	{"git", "查看或初始化项目的 Git 仓库与提交身份"}, {"add", "把一个目录加入项目列表"},
	{"scan", "在目录下查找项目"},
	{"add-version", "手动登记一个已安装的版本"},
	{"forget-version", "取消登记（不删磁盘文件）"},
	{"version", "显示版本"},
	{"help", "显示这份说明"},
}

// HasCommand reports whether name is a subcommand, so that anything else
// opens the window.
func HasCommand(name string) bool {
	for _, c := range commands {
		if c.Name == name {
			return true
		}
	}
	switch name {
	case "ls", "rm", "uninstall", "i", "status", "updates", "update", "import":
		return true
	}
	return false
}

// PrintUsage writes the help text.
func PrintUsage(w io.Writer) {
	fmt.Fprintf(w, `EnvMgr — 项目 SDK 环境管理器

用法:
  envmgr                          打开窗口
  envmgr <命令> [参数]

命令:
`)
	for _, c := range commands {
		fmt.Fprintf(w, "  %-9s %s\n", c.Name, c.Desc)
	}
	fmt.Fprintf(w, `
示例:
  eval "$(envmgr env)"            在当前 shell 里用上项目的版本
  envmgr env --shell pwsh         打印 PowerShell 版本
  envmgr run -- npm test          用项目的 Node 跑测试
  envmgr use node@20.11.1         固定 Node 版本，写入 .envmgr.json
  envmgr default node@20.11.1     目录里没写 Node 版本时默认用它
  envmgr default                  看当前的全局默认版本
  envmgr default --unset node     取消 Node 的默认版本
  envmgr list --remote node       看看能装哪些 Node 版本
  envmgr outdated                 看哪些版本落后了、哪些系列已停止维护
  envmgr upgrade --pin            升到同系列最新，并改写项目里的确切版本
  envmgr install python@3.12.4    安装 Python 3.12.4
  envmgr git                      看当前目录的仓库状态与提交身份
  envmgr git init                 把当前目录变成 Git 仓库
  envmgr git set --name 张三 --email z@ex.com
                                  只写本项目的 user.name / user.email
  envmgr git remote list --dir 项目
                                  列出项目的全部远端（含推送地址）
  envmgr git remote add gitee https://gitee.com/acme/app.git --dir 项目
                                  添加或更新一个命名远端
  envmgr git remote rm gitee --dir 项目
                                  删除一个命名远端
  envmgr git profile add github.com/acme --label 公司账号 --name 张三 --email z@acme.com
                                  同一地址可保存多套提交身份
  envmgr git profile list         列出身份 ID 与当前启用状态
  envmgr git profile use ID --dir 项目
                                  切换地址身份并写入项目的提交身份
  envmgr git profile edit ID --name 李四
                                  按 ID 修改指定身份
  envmgr git profile rm ID        删除指定身份（也兼容按地址删除）
  envmgr git rules install        让 git 按远端地址自动套用这些绑定

EnvMgr 不修改任何全局环境变量：
  · envmgr env 只输出脚本，由你的 shell 执行；
  · envmgr run 只在子进程里设置变量；
  · 项目里的 .envmgr.json 与 .nvmrc 等文件，只影响该项目。
`)
}

// Run dispatches a subcommand and returns the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stdout)
		return 0
	}
	open := core.NewService
	// The shell hook runs `env` on every directory change. It only needs what
	// the last full scan found, so it takes the cached path.
	if args[0] == "env" {
		open = core.NewFastService
	}
	svc, err := open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取配置失败:", err)
		return 1
	}
	switch args[0] {
	case "env":
		return cmdEnv(svc, args[1:])
	case "run":
		return cmdRun(svc, args[1:])
	case "use":
		return cmdUse(svc, args[1:])
	case "default":
		return cmdDefault(svc, args[1:])
	case "list", "ls":
		return cmdList(svc, args[1:])
	case "outdated", "updates":
		return cmdOutdated(svc, args[1:])
	case "upgrade", "update":
		return cmdUpgrade(svc, args[1:])
	case "install", "i":
		return cmdInstall(svc, args[1:])
	case "remove", "rm", "uninstall":
		return cmdRemove(svc, args[1:])
	case "doctor", "status":
		return cmdDoctor(svc, args[1:])
	case "hook":
		return cmdHook(svc, args[1:])
	case "git":
		return cmdGit(svc, args[1:])
	case "add":
		return cmdAdd(svc, args[1:])
	case "scan":
		return cmdScan(svc, args[1:])
	case "add-version", "import":
		return cmdAddVersion(svc, args[1:])
	case "forget-version":
		return cmdForgetVersion(svc, args[1:])
	case "version", "--version", "-v":
		fmt.Println("envmgr 1.0.0")
		return 0
	case "help", "--help", "-h":
		PrintUsage(os.Stdout)
		return 0
	}
	fmt.Fprintf(os.Stderr, "未知命令 %q\n", args[0])
	PrintUsage(os.Stderr)
	return 2
}

// flagValue pulls "--name value" or "--name=value" out of args.
func flagValue(args []string, name, def string) (string, []string) {
	var out []string
	value := def
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--"+name {
			if i+1 < len(args) {
				value = args[i+1]
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "--"+name+"=") {
			value = strings.TrimPrefix(a, "--"+name+"=")
			continue
		}
		out = append(out, a)
	}
	return value, out
}

// hasFlag reports whether a boolean flag is present.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
	}
	return false
}

// cmdEnv prints the shell code that activates the current directory.
func cmdEnv(svc *core.Service, args []string) int {
	shellName, args := flagValue(args, "shell", "")
	dir, args := flagValue(args, "dir", "")
	quiet := hasFlag(args, "quiet")

	sh, ok := core.ParseShell(shellName)
	if !ok {
		fmt.Fprintf(os.Stderr, "不认识的 shell: %s\n", shellName)
		return 2
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	env := svc.EnvForDir(dir)
	if env.Empty() {
		if len(env.Issues) > 0 {
			printEnvIssues(env.Issues)
			return 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "%s 没有版本声明\n", dir)
		}
		return 0
	}
	fmt.Print(env.ExportResolved(sh))
	// A project can ask for six SDKs and have only two installed. Exporting
	// the two that resolve beats silently dropping the whole environment; the
	// ones still missing are reported on stderr. The hook passes --quiet, so
	// only entering the project root warns instead of every subdirectory.
	if len(env.Issues) > 0 && envIssuesVisible(dir, quiet) {
		printEnvIssues(env.Issues)
	}
	return 0
}

// envIssuesVisible keeps a project with a missing version from nagging on
// every `cd` inside it. Direct calls always see the warning; the hook only
// shows it at the directory that declares the versions.
func envIssuesVisible(dir string, quiet bool) bool {
	if !quiet {
		return true
	}
	root, ok := core.NearestProjectDir(dir)
	return ok && core.SamePath(dir, root)
}

func printEnvIssues(issues []string) {
	for _, issue := range issues {
		fmt.Fprintln(os.Stderr, "envmgr: "+issue)
	}
}

// cmdRun runs a command inside the project's environment.
func cmdRun(svc *core.Service, args []string) int {
	dir, rest := flagValue(args, "dir", "")
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "用法: envmgr run -- <命令> [参数...]")
		return 2
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	env := svc.EnvForDir(dir)
	code, err := core.RunCommand(dir, env, rest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return code
}

// cmdUse pins versions into the current directory's .envmgr.json.
func cmdUse(svc *core.Service, args []string) int {
	dir, args := flagValue(args, "dir", "")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if len(args) == 0 {
		// Show what the directory resolves to.
		env := svc.EnvForDir(dir)
		if env.Empty() {
			fmt.Println("没有固定任何版本")
			return 0
		}
		for _, line := range env.Describe() {
			fmt.Println(line)
		}
		return 0
	}
	installed := svc.Installed()
	pins := map[core.Kind]string{}
	for _, arg := range args {
		kindName, version, ok := strings.Cut(arg, "@")
		if !ok {
			kindName, version = arg, ""
		}
		kind, ok := core.ParseKind(kindName)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", kindName)
			return 2
		}
		if version == "" {
			// "envmgr use node" pins whatever the machine has newest.
			best, found := core.Best(installed, kind, "")
			if !found {
				fmt.Fprintf(os.Stderr, "本机没有 %s 的任何版本\n", core.Info(kind).Name)
				return 1
			}
			version = best.Version
		} else {
			if _, found := core.Best(installed, kind, version); !found {
				fmt.Fprintf(os.Stderr, "提示：本机还没有 %s %s，可直接 envmgr install %s@%s\n",
					core.Info(kind).Name, version, kind, version)
			}
		}
		pins[kind] = version
		fmt.Printf("%s 固定为 %s\n", core.Info(kind).Name, version)
	}
	if err := core.UpdateProjectPins(dir, pins); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("已写入 %s\n", filepath.Join(dir, core.ConfigFileName))
	return 0
}

// cmdDefault reads or changes the versions used where a directory says
// nothing. Without one EnvMgr is silent outside projects, which is a strange
// thing for a shell helper: you fix Java once and it is there everywhere.
func cmdDefault(svc *core.Service, args []string) int {
	unset, args := flagValue(args, "unset", "")
	code := 0
	attempted := unset != ""
	if unset != "" {
		kind, ok := core.ParseKind(unset)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", unset)
			return 2
		}
		if err := svc.ClearDefault(kind); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已取消 %s 的全局默认版本\n", core.Info(kind).Name)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			continue
		}
		attempted = true
		kindName, expr, ok := strings.Cut(arg, "@")
		if !ok || strings.TrimSpace(expr) == "" {
			fmt.Fprintln(os.Stderr, "用法: envmgr default node@20.11.1 | --unset node")
			code = 2
			continue
		}
		kind, ok := core.ParseKind(kindName)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", kindName)
			code = 2
			continue
		}
		if err := svc.SetDefault(kind, expr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
			continue
		}
		installed, _ := core.Best(svc.Installed(), kind, expr)
		fmt.Printf("%s 默认使用 %s（项目里写了版本时仍以项目为准）\n", core.Info(kind).Name, installed.Version)
	}
	if attempted {
		return code
	}

	defaults := svc.Defaults()
	installed := svc.Installed()
	printed := false
	for _, kind := range core.Kinds {
		expr := defaults[kind]
		if expr == "" {
			continue
		}
		printed = true
		if inst, found := core.Best(installed, kind, expr); found {
			fmt.Printf("  %-8s %s\n", core.Info(kind).Name, inst.Version)
		} else {
			fmt.Printf("  %-8s %s（本机已没有，默认不生效）\n", core.Info(kind).Name, expr)
		}
	}
	if !printed {
		fmt.Println("还没有全局默认版本。用法: envmgr default node@20.11.1")
	}
	return 0
}

// cmdList lists installed versions, or downloadable ones with --remote.
func cmdList(svc *core.Service, args []string) int {
	remote := hasFlag(args, "remote")
	var kinds []core.Kind
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			continue
		}
		if kind, ok := core.ParseKind(a); ok {
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) == 0 {
		kinds = core.Kinds
	}

	if remote {
		for _, kind := range kinds {
			list, err := svc.ListRemote(context.Background(), kind, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", core.Info(kind).Name, err)
				continue
			}
			fmt.Printf("%s\n", core.Info(kind).Name)
			for _, r := range list {
				// The vendor matters for Java, where one version exists as
				// several builds; show it in the label column.
				label := r.Label
				if r.VendorName != "" {
					if label == "" {
						label = r.VendorName
					} else {
						label = r.VendorName + " · " + label
					}
				}
				if label == "" {
					label = "-"
				}
				size := core.HumanSize(r.Size)
				if r.Size <= 0 {
					size = "-"
				}
				fmt.Printf("  %-14s %-34s %s\n", r.Version, label, size)
			}
		}
		return 0
	}

	installed := svc.Installed()
	for _, kind := range kinds {
		list := core.InstalledOf(installed, kind)
		if len(list) == 0 {
			continue
		}
		fmt.Printf("%s\n", core.Info(kind).Name)
		for _, inst := range list {
			manager := inst.Manager
			if manager == "" {
				manager = "未知"
			}
			fmt.Printf("  %-14s %-10s %s\n", inst.Version, manager, inst.Path)
		}
	}
	return 0
}

// cmdInstall downloads a version.
func cmdInstall(svc *core.Service, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: envmgr install node@20.11.1 [python@3.12.4 ...]")
		return 2
	}
	code := 0
	for _, arg := range args {
		kindName, rest, ok := strings.Cut(arg, "@")
		if !ok {
			fmt.Fprintf(os.Stderr, "%q 需要写成 名称@版本，例如 node@20.11.1\n", arg)
			code = 2
			continue
		}
		// java@21.0.13@corretto picks the vendor; without it the default build
		// is used, so one version never installs a random vendor's build.
		version, wantVendor, _ := strings.Cut(rest, "@")
		kind, ok := core.ParseKind(kindName)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", kindName)
			code = 2
			continue
		}
		list, err := svc.ListRemote(context.Background(), kind, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", core.Info(kind).Name, err)
			code = 1
			continue
		}
		if wantVendor != "" {
			if core.JavaVendorHome(wantVendor) == "" {
				fmt.Fprintf(os.Stderr, "不认识的下载源: %s（可用：%s）\n",
					wantVendor, strings.Join(vendorIDs(), "、"))
				code = 2
				continue
			}
		}
		target := pickRemote(list, version, wantVendor)
		if target == nil {
			fmt.Fprintf(os.Stderr, "没有找到 %s %s\n", core.Info(kind).Name, version)
			code = 1
			continue
		}
		fmt.Printf("安装 %s %s", core.Info(kind).Name, target.Version)
		if target.VendorName != "" {
			fmt.Printf("（%s）", target.VendorName)
		}
		fmt.Println(" …")
		lastLine := ""
		inst, err := svc.Install(context.Background(), *target, func(done, total int64) {
			if total > 0 {
				line := fmt.Sprintf("\r  %3d%%  %s / %s", done*100/total, core.HumanSize(done), core.HumanSize(total))
				if line != lastLine {
					fmt.Print(line)
					lastLine = line
				}
			}
		})
		fmt.Print("\r" + strings.Repeat(" ", 60) + "\r")
		if err != nil {
			fmt.Fprintf(os.Stderr, "失败: %v\n", err)
			code = 1
			continue
		}
		fmt.Printf("完成: %s %s → %s\n", core.Info(inst.Kind).Name, inst.Version, inst.Path)
	}
	return code
}

// pickRemote chooses which build to install. An exact version wins over a
// prefix, and within a version: the requested vendor, then the default vendor,
// then whoever came first — so `install java@21` never silently installs a
// random vendor's build.
func pickRemote(list []core.Remote, version, wantVendor string) *core.Remote {
	if wantVendor != "" {
		// The vendor was named: use it for an exact version, else its newest
		// build matching the prefix.
		for i := range list {
			if list[i].Version == version && list[i].Vendor == wantVendor {
				return &list[i]
			}
		}
		for i := range list {
			if strings.HasPrefix(list[i].Version, version) && list[i].Vendor == wantVendor {
				return &list[i]
			}
		}
		return nil
	}
	// 1. exact version from the default vendor
	for i := range list {
		if list[i].Version == version && (list[i].Vendor == core.DefaultVendor || list[i].Vendor == "") {
			return &list[i]
		}
	}
	// 2. any exact version
	for i := range list {
		if list[i].Version == version {
			return &list[i]
		}
	}
	// 3. a prefix, e.g. java@21 for 21.0.13, again preferring the default
	for i := range list {
		if strings.HasPrefix(list[i].Version, version) &&
			(list[i].Vendor == core.DefaultVendor || list[i].Vendor == "") {
			return &list[i]
		}
	}
	for i := range list {
		if strings.HasPrefix(list[i].Version, version) {
			return &list[i]
		}
	}
	return nil
}

// vendorIDs lists the vendor ids the command line accepts.
func vendorIDs() []string {
	out := make([]string, 0, len(core.JavaVendors()))
	for _, v := range core.JavaVendors() {
		out = append(out, v.ID)
	}
	return out
}

// cmdRemove deletes a version EnvMgr installed.
func cmdRemove(svc *core.Service, args []string) int {
	force := hasFlag(args, "force")
	specs := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			continue
		}
		specs = append(specs, a)
	}
	if len(specs) == 0 {
		fmt.Fprintln(os.Stderr, "用法: envmgr remove node@20.11.1 [--force]")
		fmt.Fprintln(os.Stderr, "      --force 仍被项目使用时也删除")
		return 2
	}
	code := 0
	for _, arg := range specs {
		kindName, version, ok := strings.Cut(arg, "@")
		if !ok {
			fmt.Fprintf(os.Stderr, "%q 需要写成 名称@版本\n", arg)
			code = 2
			continue
		}
		kind, ok := core.ParseKind(kindName)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", kindName)
			code = 2
			continue
		}
		inst, found := core.Exact(svc.Installed(), kind, version)
		if !found {
			fmt.Fprintf(os.Stderr, "本机没有 %s %s\n", core.Info(kind).Name, version)
			code = 1
			continue
		}
		remove := svc.Remove
		if force {
			remove = svc.RemoveForced
		}
		if err := remove(inst); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			code = 1
			continue
		}
		fmt.Printf("已删除 %s\n", inst.ID())
	}
	return code
}

// cmdDoctor reports what each project needs and what is missing.
func cmdDoctor(svc *core.Service, args []string) int {
	if dir, _ := flagValue(args, "dir", ""); dir != "" {
		return doctorDir(svc, dir)
	}
	rep := core.Diagnose(svc)
	for _, sec := range rep.Sections {
		if len(sec.Checks) == 0 {
			continue
		}
		fmt.Printf("\n%s\n", sec.Title)
		for _, c := range sec.Checks {
			fmt.Printf("  %s %-22s %s\n", doctorMark(c.Level), c.Title, c.Detail)
			if c.Hint != "" && c.Level != core.LevelOK {
				fmt.Printf("       %s\n", c.Hint)
			}
		}
	}
	fmt.Printf("\n%s\n", rep.Summary())
	if !rep.Healthy() {
		return 1
	}
	return 0
}

// doctorMark is the status glyph for a check.
func doctorMark(l core.Level) string {
	switch l {
	case core.LevelOK:
		return "✓"
	case core.LevelWarn:
		return "!"
	case core.LevelFail:
		return "✗"
	}
	return "·"
}

// doctorDir reports how one directory resolves, for scripting.
func doctorDir(svc *core.Service, dir string) int {
	path, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	requested := path
	// A subdirectory inherits its project root, so report the directory that
	// actually decides the versions rather than "没有版本声明".
	if root, ok := core.NearestProjectDir(path); ok {
		path = root
	}
	p := core.NewProject(path)
	res := svc.Resolve(p)
	fmt.Printf("%s\n", path)
	if !core.SamePath(path, requested) {
		fmt.Printf("  · 从 %s 向上找到这个项目\n", requested)
	}
	if len(res) == 0 {
		fmt.Println("  · 没有版本声明")
		return 0
	}
	missing := 0
	for _, r := range res {
		if r.Err != nil {
			fmt.Fprintln(os.Stderr, "项目配置错误：", r.Err)
			return 1
		}
		info := core.Info(r.Kind)
		if r.Missing {
			missing++
			fmt.Printf("  ✗ %-8s %-12s 未安装（来源 %s）\n", info.Name, r.Expr, r.Source)
			continue
		}
		fmt.Printf("  ✓ %-8s %-12s %s（来源 %s）\n", info.Name, r.Installed.Version, r.Installed.Manager, r.Source)
	}
	if missing > 0 {
		fmt.Printf("\n%d 个版本缺失，可用 envmgr install <名称>@<版本> 安装。\n", missing)
		return 1
	}
	return 0
}

// cmdGit reports repository state and dispatches repository, remote, identity,
// and opt-in global rule commands.
func cmdGit(svc *core.Service, args []string) int {
	for i, arg := range args {
		switch arg {
		case "--dir", "--name", "--email", "--label":
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintf(os.Stderr, "%s 需要一个参数值\n", arg)
				return 2
			}
		}
	}
	dir, args := flagValue(args, "dir", "")
	name, args := flagValue(args, "name", "")
	email, args := flagValue(args, "email", "")
	label, args := flagValue(args, "label", "")

	// The first bare word is a subcommand only when it is one: otherwise
	// `envmgr git cf` would report an unknown subcommand instead of reading the
	// project named cf.
	action := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && isGitAction(args[0]) {
		action = args[0]
		args = args[1:]
	}
	// Resolve --dir independently: profile IDs and remote names are positional
	// arguments, and must never be interpreted as project directories.
	if dir != "" {
		dir = resolveDir(svc, dir)
	}
	switch action {
	case "profile":
		return cmdGitProfile(svc, args, dir, label, name, email)
	case "remote":
		if dir == "" {
			dir, _ = os.Getwd()
		}
		return cmdGitRemote(svc, args, dir)
	case "rules":
		return cmdGitRules(svc, args)
	}
	// A bare argument names a project or a directory. Flags are skipped:
	// otherwise "--clear" would be read as a directory name, which is exactly
	// the kind of mistake that writes to a relative path nobody expects.
	if dir == "" {
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			dir = resolveDir(svc, a)
			break
		}
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}

	switch action {
	case "", "status":
		info := svc.Git(dir)
		if !info.Available {
			fmt.Fprintln(os.Stderr, "没有找到 git，请先安装 Git: https://git-scm.com/downloads")
			return 1
		}
		fmt.Printf("目录  %s\n", dir)
		if !info.Repo {
			fmt.Println("仓库  不是 Git 仓库（envmgr git init 可初始化）")
		} else {
			fmt.Println("仓库  " + info.Root)
			if info.Detached {
				fmt.Println("分支  游离 HEAD")
			} else {
				fmt.Println("分支  " + info.Branch)
			}
			printGitRemotes(info.Remotes)
			if !info.Commits {
				fmt.Println("提交  还没有提交")
			} else if info.Clean() {
				fmt.Println("改动  没有未提交的改动")
			} else {
				fmt.Printf("改动  %s\n", gitChangeText(info))
			}
		}
		printGitIdentity(info.Identity)
		return 0

	case "init":
		info, err := core.InitGitRepo(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已初始化 %s（分支 %s）\n", info.Root, info.Branch)
		printGitIdentity(info.Identity)
		return 0

	case "set":
		clear := hasFlag(args, "clear")
		if !clear && name == "" && email == "" {
			fmt.Fprintln(os.Stderr, "用法: envmgr git set [--name 名字] [--email 邮箱] [--clear] [--dir 目录]")
			fmt.Fprintln(os.Stderr, "      --clear 删除本项目的设置，回到全局的 user.name / user.email")
			return 2
		}
		if err := core.WriteGitIdentity(dir, name, email); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if clear {
			fmt.Printf("已删除本项目在 %s 里的 user.name / user.email\n", filepath.Join(dir, ".git", "config"))
		} else {
			fmt.Printf("已写入 %s\n", filepath.Join(dir, ".git", "config"))
		}
		printGitIdentity(core.LoadGit(dir).Identity)
		return 0

	}

	fmt.Fprintf(os.Stderr, "不认识的 git 子命令 %q\n", action)
	fmt.Fprintln(os.Stderr, "可用: envmgr git [status] | envmgr git init | envmgr git set --name X --email Y")
	fmt.Fprintln(os.Stderr, "      envmgr git remote list | add 名称 URL | rm 名称 [--dir 项目]")
	fmt.Fprintln(os.Stderr, "      envmgr git profile list | add | edit ID | use ID [--dir 项目] | rm ID或地址")
	fmt.Fprintln(os.Stderr, "      envmgr git rules install | uninstall")
	return 2
}

// cmdGitProfile manages individually identified identities at remote addresses.
func cmdGitProfile(svc *core.Service, args []string, dir, label, name, email string) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "", "list":
		profiles := svc.GitProfiles()
		if len(profiles) == 0 {
			fmt.Println("还没有绑定。用 envmgr git profile add 地址 --name 名字 --email 邮箱 新建一条。")
			return 0
		}
		for _, p := range profiles {
			state := "未启用"
			if p.Active {
				state = "已启用"
			}
			fmt.Printf("%s  [%s] %-14s %s  <%s>\n", p.IdentityID(), state, p.Display(), p.Name, p.Email)
			fmt.Printf("    %s\n", p.Key)
		}
		if svc.GitRulesInstalled() {
			fmt.Println("\n自动套用  已安装")
		} else {
			fmt.Println("\n自动套用  未安装（envmgr git rules install）")
		}
		fmt.Printf("规则文件  %s\n", core.GlobalGitConfig())
		return 0

	case "use":
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			fmt.Fprintln(os.Stderr, "用法: envmgr git profile use ID [--dir 项目或目录]")
			return 2
		}
		if dir == "" {
			dir, _ = os.Getwd()
		}
		if err := svc.SelectGitProfile(dir, args[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已选择身份 %s → %s\n", args[0], dir)
		printGitIdentity(svc.Git(dir).Identity)
		return 0

	case "edit":
		if len(args) != 1 || strings.HasPrefix(args[0], "-") || (name == "" && email == "" && label == "") {
			fmt.Fprintln(os.Stderr, "用法: envmgr git profile edit ID [--name 名字] [--email 邮箱] [--label 显示名]")
			return 2
		}
		for _, p := range svc.GitProfiles() {
			if p.IdentityID() != args[0] {
				continue
			}
			p.ID = p.IdentityID()
			if name != "" {
				p.Name = name
			}
			if email != "" {
				p.Email = email
			}
			if label != "" {
				p.Label = label
			}
			if err := svc.SaveGitProfile(p); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Printf("已修改身份 %s → %s <%s>\n", p.IdentityID(), p.Name, p.Email)
			return 0
		}
		fmt.Fprintf(os.Stderr, "没有找到身份 %q\n", args[0])
		return 1

	case "add", "set":
		// The first bare word is the address.
		key := ""
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			key = a
			break
		}
		if len(args) != 1 || key == "" || name == "" || email == "" {
			fmt.Fprintln(os.Stderr, "用法: envmgr git profile add 地址 --name 名字 --email 邮箱 [--label 显示名]")
			fmt.Fprintln(os.Stderr, "      地址可以写到组织一级，例如 github.com/acme")
			return 2
		}
		normalized, ok := core.NormalizeRemote(key)
		if !ok {
			fmt.Fprintf(os.Stderr, "%q 看起来不是远端地址，例如 github.com/acme 或 git@github.com:acme/app.git\n", key)
			return 2
		}
		if err := svc.SaveGitProfile(core.GitProfile{
			Key: normalized, Label: label, Name: name, Email: email,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已绑定 %s → %s <%s>\n", normalized, name, email)
		if !svc.GitRulesInstalled() {
			fmt.Println("提示: 还没安装自动套用规则，执行 envmgr git rules install 后 git 才会按地址自动选择身份。")
		}
		return 0

	case "rm", "remove", "delete":
		key := ""
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			key = a
			break
		}
		if len(args) != 1 || key == "" {
			fmt.Fprintln(os.Stderr, "用法: envmgr git profile rm ID或地址")
			return 2
		}
		normalized := key
		if k, ok := core.NormalizeRemote(key); ok {
			normalized = k
		}
		if err := svc.DeleteGitProfile(normalized); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已删除绑定 %s\n", normalized)
		return 0
	}

	fmt.Fprintf(os.Stderr, "不认识的 git profile 子命令 %q\n", sub)
	fmt.Fprintln(os.Stderr, "可用: envmgr git profile list | add | edit ID | use ID [--dir 项目] | rm ID或地址")
	return 2
}

// cmdGitRules installs or removes the include block in the global git config.
func cmdGitRules(svc *core.Service, args []string) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
	}
	switch sub {
	case "status":
		if svc.GitRulesInstalled() {
			fmt.Println("自动套用  已安装")
		} else {
			fmt.Println("自动套用  未安装")
		}
		fmt.Printf("规则文件  %s\n", core.GlobalGitConfig())
		major, minor, ok := core.GitVersion()
		if ok {
			fmt.Printf("git 版本  %d.%d\n", major, minor)
		}
		if !core.IncludeIfByRemoteSupported() {
			fmt.Println("注意  git 低于 2.36，不支持按远端地址自动套用；可以用 envmgr git set 写进单个仓库。")
		}
		return 0

	case "install":
		if len(svc.GitProfiles()) == 0 {
			fmt.Fprintln(os.Stderr, "还没有任何绑定，先 envmgr git profile add。")
			return 2
		}
		if err := svc.InstallGitRules(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已写入 %s（原文件备份为 .envmgr.bak）\n", core.GlobalGitConfig())
		return 0

	case "uninstall":
		if err := svc.UninstallGitRules(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("已从全局 git 配置里移除 EnvMgr 的规则块")
		return 0
	}

	fmt.Fprintf(os.Stderr, "不认识的 git rules 子命令 %q\n", sub)
	fmt.Fprintln(os.Stderr, "可用: envmgr git rules status | install | uninstall")
	return 2
}

// isGitAction reports whether a word is a git subcommand rather than a project
// or directory name.
func isGitAction(s string) bool {
	switch s {
	case "status", "init", "set", "remote", "profile", "rules":
		return true
	}
	return false
}

// resolveDir turns a project name, a project path, or an existing directory into
// an absolute directory.
func resolveDir(svc *core.Service, arg string) string {
	if abs, err := filepath.Abs(arg); err == nil {
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			return abs
		}
	}
	for _, p := range svc.Projects() {
		if strings.EqualFold(p.Name, arg) || strings.EqualFold(p.Path, arg) {
			return p.Path
		}
	}
	return arg
}

// gitChangeText words the counts the same way the window does.
func gitChangeText(g core.GitInfo) string {
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

// printGitIdentity shows the effective identity and where each value came from.
func printGitIdentity(id core.GitIdentity) {
	source := func(s core.Source) string {
		switch s {
		case core.SourceProject:
			return "本项目"
		case core.SourceRule:
			return "远端绑定"
		case core.SourceGlobal:
			return "全局"
		default:
			return "未设置"
		}
	}
	name, email := id.Name, id.Email
	if name == "" {
		name = "—"
	}
	if email == "" {
		email = "—"
	}
	fmt.Printf("用户名  %s（%s）\n", name, source(id.NameSource))
	fmt.Printf("邮箱    %s（%s）\n", email, source(id.EmailSource))
	if !id.Complete() {
		fmt.Println("提示：用户名或邮箱缺失，git commit 会拒绝执行。")
	}
}

// cmdHook prints or installs the opt-in shell hook.
func cmdHook(svc *core.Service, args []string) int {
	install := hasFlag(args, "install")
	uninstall := hasFlag(args, "uninstall")
	name := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		name = a
		break
	}
	sh, ok := core.ParseShell(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "不认识的 shell: %s\n", name)
		return 2
	}

	switch {
	case install:
		file, err := core.InstallHook(sh)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已写入 %s\n", file)
		fmt.Println("新开一个终端即可生效；进入带版本声明的项目时自动切换，离开时还原。")
		fmt.Println("原来的文件已备份为 " + file + ".envmgr.bak")
		return 0
	case uninstall:
		removed, err := core.UninstallHook(sh)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if !removed {
			fmt.Printf("%s 里没有 EnvMgr 的钩子\n", core.HookFile(sh))
			return 0
		}
		fmt.Printf("已从 %s 移除\n", core.HookFile(sh))
		return 0
	}

	state := "未安装"
	if core.HookInstalled(sh) {
		state = "已安装"
	}
	fmt.Printf("# 钩子状态：%s\n", state)
	for _, f := range core.HookFiles(sh) {
		mark := "未装"
		if core.HookInstalledIn(f) {
			mark = "已装"
		}
		fmt.Printf("#   [%s] %s\n", mark, f)
	}
	fmt.Printf("# 安装: envmgr hook --install %s\n", sh)
	fmt.Printf("# 移除: envmgr hook --uninstall %s\n", sh)
	fmt.Println("# 下面这段就是会被写进去的内容：")
	fmt.Print(core.HookScript(sh))
	return 0
}

// cmdAdd adds a directory to the project list.
func cmdAdd(svc *core.Service, args []string) int {
	if len(args) == 0 {
		dir, _ := os.Getwd()
		args = []string{dir}
	}
	for _, dir := range args {
		p, err := svc.AddProject(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", dir, err)
			return 1
		}
		fmt.Printf("已添加 %s\n", p.Path)
	}
	return 0
}

// cmdScan looks for projects under a directory.
func cmdScan(svc *core.Service, args []string) int {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	found := core.ScanProjects(abs, 3)
	if len(found) == 0 {
		fmt.Println("没有找到项目目录")
		return 0
	}
	for _, dir := range found {
		if _, ok := svc.Store().ProjectByPath(dir); ok {
			fmt.Printf("· %s（已在列表中）\n", dir)
			continue
		}
		fmt.Println(dir)
	}
	fmt.Printf("\n共 %d 个，用 envmgr add <目录> 加入列表。\n", len(found))
	return 0
}

// cmdAddVersion registers a version that lives outside the usual places, the
// command-line half of 工具链 ▸ 添加已安装版本.
func cmdAddVersion(svc *core.Service, args []string) int {
	version, args := flagValue(args, "version", "")
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: envmgr add-version <sdk> <目录> [版本]\n例如: envmgr add-version java ~/env/java/17 17.0.17")
		return 2
	}
	kind, ok := core.ParseKind(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", args[0])
		return 2
	}
	dir := args[1]
	if version == "" && len(args) > 2 {
		version = args[2]
	}
	inst, err := svc.AddManualInstall(kind, version, dir, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("已登记 %s %s → %s\n", core.Info(inst.Kind).Name, inst.Version, inst.Path)
	return 0
}

// cmdForgetVersion stops listing a hand-added version.
func cmdForgetVersion(svc *core.Service, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: envmgr forget-version <sdk> <目录>")
		return 2
	}
	kind, ok := core.ParseKind(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", args[0])
		return 2
	}
	if err := svc.RemoveManualInstall(kind, args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("已取消登记 %s\n", args[1])
	return 0
}
