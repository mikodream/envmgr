package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"envmgr/internal/core"
)

// pad right-pads s to w terminal cells. CJK counts as two cells, so the
// tables stay aligned regardless of which language a status is written in.
func pad(s string, w int) string {
	n := 0
	for _, r := range s {
		if wideRune(r) {
			n += 2
		} else {
			n++
		}
	}
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func wideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6:
		return true
	}
	return false
}

// eolLabel is the maintenance column: a date when the vendor published one.
func eolLabel(u core.Update) string {
	switch u.EOLState {
	case core.EOLEnded:
		if u.EOL != "" {
			return u.EOL + " 已停止"
		}
		return "已停止维护"
	case core.EOLSoon:
		return fmt.Sprintf("%d 天后停止", u.DaysLeft)
	case core.EOLActive:
		if u.EOL != "" {
			return u.EOL + " 结束"
		}
		return "维护中"
	}
	return "—"
}

func levelLabel(u core.Update) string {
	if u.Latest == "" {
		return "—"
	}
	if !u.Outdated() {
		return "最新"
	}
	return u.Level.String() + "更新"
}

// cmdOutdated lists installed versions next to the newest release of their
// own line, plus the vendor's end-of-life date.
func cmdOutdated(svc *core.Service, args []string) int {
	force := hasFlag(args, "refresh") || hasFlag(args, "check")
	asJSON := hasFlag(args, "json")
	want := map[core.Kind]bool{}
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			continue
		}
		kind, ok := core.ParseKind(a)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", a)
			return 2
		}
		want[kind] = true
	}

	updates, err := svc.Updates(context.Background(), force)
	if err != nil {
		// A single unreachable source must not hide the rest.
		fmt.Fprintln(os.Stderr, "提示:", err)
	}
	if len(want) > 0 {
		filtered := updates[:0]
		for _, u := range updates {
			if want[u.Kind] {
				filtered = append(filtered, u)
			}
		}
		updates = filtered
	}

	if asJSON {
		type row struct {
			Kind     string `json:"kind"`
			Version  string `json:"version"`
			Manager  string `json:"manager"`
			Latest   string `json:"latest,omitempty"`
			Newest   string `json:"newest,omitempty"`
			Level    string `json:"level"`
			EOL      string `json:"eol,omitempty"`
			EOLState string `json:"eolState"`
			DaysLeft int    `json:"daysLeft,omitempty"`
			Path     string `json:"path"`
		}
		rows := make([]row, 0, len(updates))
		for _, u := range updates {
			rows = append(rows, row{
				Kind: string(u.Kind), Version: u.Version, Manager: u.Manager,
				Latest: u.Latest, Newest: u.Newest, Level: u.Level.Token(),
				EOL: u.EOL, EOLState: u.EOLState.Token(), DaysLeft: u.DaysLeft, Path: u.Path,
			})
		}
		b, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}

	if len(updates) == 0 {
		fmt.Println("本机还没有安装任何 SDK。")
		return 0
	}

	fmt.Printf("%s %s %s %s %s %s\n",
		pad("SDK", 18), pad("当前", 15), pad("系列最新", 15), pad("更新", 10), pad("新系列", 12), "维护")
	outdated, ended := 0, 0
	for _, u := range updates {
		if u.Outdated() {
			outdated++
		}
		if u.EOLState == core.EOLEnded {
			ended++
		}
		latest, newest := u.Latest, ""
		if latest == "" {
			latest = "—"
		}
		if u.HasNewerLine() {
			newest = u.Newest
		}
		name := core.Info(u.Kind).Name
		if u.Manager != "" && u.Manager != "envmgr" {
			name += " ·" + u.Manager
		}
		fmt.Printf("%s %s %s %s %s %s\n",
			pad(name, 18), pad(u.Version, 15), pad(latest, 15), pad(levelLabel(u), 10), pad(newest, 12), eolLabel(u))
	}

	fmt.Println()
	switch {
	case outdated == 0 && ended == 0:
		fmt.Println("全部都是各自系列的最新版本。")
	case outdated == 0:
		fmt.Printf("没有可更新的版本；%d 个系列已停止维护。\n", ended)
	default:
		fmt.Printf("%d 个版本可更新", outdated)
		if ended > 0 {
			fmt.Printf("；%d 个系列已停止维护", ended)
		}
		fmt.Println("。用 envmgr upgrade 升级 EnvMgr 安装的版本。")
	}
	return 0
}

// upgradeTargets resolves which installed versions the user asked for. The
// arguments are validated before the installed list is read, so a typo costs
// nothing.
func upgradeTargets(svc *core.Service, updates []core.Update, args []string) ([]core.Update, int) {
	if len(args) == 0 {
		var out []core.Update
		for _, u := range updates {
			if u.Outdated() {
				out = append(out, u)
			}
		}
		return out, 0
	}
	type want struct {
		kind core.Kind
		expr string
	}
	var wants []want
	code := 0
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			continue
		}
		kindName, expr, ok := strings.Cut(arg, "@")
		if !ok || strings.TrimSpace(expr) == "" {
			fmt.Fprintf(os.Stderr, "%q 需要写成 名称@版本，例如 node@20\n", arg)
			code = 2
			continue
		}
		kind, ok := core.ParseKind(kindName)
		if !ok {
			fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", kindName)
			code = 2
			continue
		}
		wants = append(wants, want{kind: kind, expr: expr})
	}
	if len(wants) == 0 || svc == nil {
		return nil, code
	}
	installed := svc.Installed()
	var out []core.Update
	for _, w := range wants {
		inst, found := core.Best(installed, w.kind, w.expr)
		if !found {
			fmt.Fprintf(os.Stderr, "本机没有匹配 %s %s 的版本\n", core.Info(w.kind).Name, w.expr)
			code = 1
			continue
		}
		var matched bool
		for _, u := range updates {
			if u.Kind == inst.Kind && u.Version == inst.Version {
				out = append(out, u)
				matched = true
				break
			}
		}
		if !matched {
			fmt.Fprintf(os.Stderr, "无法判断 %s %s 的更新状态\n", core.Info(w.kind).Name, inst.Version)
			code = 1
		}
	}
	return out, code
}

// cmdUpgrade installs the newest release of each selected line.
func cmdUpgrade(svc *core.Service, args []string) int {
	dryRun := hasFlag(args, "dry-run")
	pin := hasFlag(args, "pin")

	updates, err := svc.Updates(context.Background(), false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "提示:", err)
	}

	targets, code := upgradeTargets(svc, updates, args)
	if code != 0 {
		return code
	}
	if len(targets) == 0 {
		fmt.Println("没有需要升级的版本。")
		return 0
	}

	type planned struct {
		update core.Update
		target core.Remote
	}
	var plan []planned
	for _, u := range targets {
		if u.Kind == core.Rust {
			fmt.Fprintf(os.Stderr, "跳过 Rust %s：工具链由 rustup 升级（stable 频道现在是 %s）\n", u.Version, u.Latest)
			continue
		}
		if u.Manager != "envmgr" {
			manager := u.Manager
			if manager == "" {
				manager = "其他工具"
			}
			fmt.Fprintf(os.Stderr, "跳过 %s %s：由 %s 管理，请用该工具升级\n",
				core.Info(u.Kind).Name, u.Version, manager)
			continue
		}
		if !u.Outdated() {
			fmt.Printf("%s %s 已经是 %s 系列最新", core.Info(u.Kind).Name, u.Version, u.Latest)
			if u.HasNewerLine() {
				fmt.Printf("（更新的系列是 %s，跨系列请显式安装）", u.Newest)
			}
			fmt.Println()
			continue
		}
		inst, found := core.Exact(svc.Installed(), u.Kind, u.Version)
		if !found {
			continue
		}
		remote, err := svc.UpgradeTarget(context.Background(), inst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			code = 1
			continue
		}
		plan = append(plan, planned{update: u, target: remote})
	}

	for _, p := range plan {
		info := core.Info(p.update.Kind)
		if dryRun {
			fmt.Printf("将升级 %s %s → %s（%s）\n", info.Name, p.update.Version, p.target.Version, p.update.Level)
			continue
		}
		old, _ := core.Exact(svc.Installed(), p.update.Kind, p.update.Version)
		fmt.Printf("升级 %s %s → %s …\n", info.Name, p.update.Version, p.target.Version)
		var lastLine string
		installed, err := svc.Install(context.Background(), p.target, func(done, total int64) {
			if total <= 0 {
				return
			}
			line := fmt.Sprintf("\r  %3d%%  %s / %s", done*100/total, core.HumanSize(done), core.HumanSize(total))
			if line != lastLine {
				fmt.Print(line)
				lastLine = line
			}
		})
		fmt.Print("\r" + strings.Repeat(" ", 60) + "\r")
		if err != nil {
			fmt.Fprintf(os.Stderr, "升级失败: %v\n", err)
			code = 1
			continue
		}
		fmt.Printf("完成: %s %s → %s\n", info.Name, p.update.Version, installed.Version)
		reportLeftovers(svc, old, installed, pin)
	}
	return code
}

// reportLeftovers says which projects still resolve to the old version and,
// with pin set, rewrites the exact pins.
func reportLeftovers(svc *core.Service, old, now core.Installed, pin bool) {
	users := svc.VersionUsers(old.Kind, old.Version)
	if pin {
		changed, err := svc.RepinExact(old, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "改项目固定版本失败:", err)
		}
		for _, p := range changed {
			fmt.Printf("  已把 %s 的固定版本改为 %s\n", p.Name, now.Version)
		}
		if len(changed) > 0 {
			users = svc.VersionUsers(old.Kind, old.Version)
		}
	}
	if len(users) == 0 {
		return
	}
	fmt.Printf("  仍有 %d 个项目固定着 %s %s：", len(users), core.Info(old.Kind).Name, old.Version)
	names := make([]string, 0, len(users))
	for _, p := range users {
		names = append(names, p.Name)
	}
	fmt.Println(strings.Join(names, "、"))
	if !pin {
		fmt.Println("  项目里写的是确切版本时，用 --pin 一并改写；写的是 20 这类前缀会随新版本生效。")
	}
}
