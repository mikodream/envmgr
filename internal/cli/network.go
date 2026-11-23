package cli

import (
	"fmt"
	"os"
	"strings"

	"envmgr/internal/core"
)

// cmdNet shows and edits where downloads come from.
//
// Two settings sit behind it: an HTTP proxy that applies to every request, and
// a per-SDK mirror base that replaces the vendor's own servers. Both exist
// because the vendors' addresses are the slow part in mainland China, and no
// amount of retrying fixes that.
func cmdNet(svc *core.Service, args []string) int {
	if len(args) == 0 {
		return netStatus(svc)
	}
	cfg := svc.Config()
	switch strings.ToLower(args[0]) {
	case "proxy":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "用法: envmgr net proxy auto|direct|http://127.0.0.1:7890")
			return 2
		}
		value, err := core.NormalizeProxy(strings.Join(args[1:], " "))
		if err != nil {
			fmt.Fprintf(os.Stderr, "代理地址无效: %v\n", err)
			return 2
		}
		cfg.Proxy = value
		if err := svc.SetConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Printf("代理已设为 %s\n", core.ProxyLabel(value))
		return 0
	}

	kind, ok := core.ParseKind(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "不认识的 SDK: %s\n", args[0])
		return 2
	}
	if len(core.MirrorPresets(kind)) == 0 && core.CanonicalBase(kind) == "" {
		fmt.Fprintf(os.Stderr, "%s 没有内置镜像，改用代理: envmgr net proxy http://127.0.0.1:7890\n", core.Info(kind).Name)
		return 2
	}
	if len(args) < 2 {
		for _, m := range cfg.MirrorSummaries() {
			if m.Kind == kind {
				fmt.Printf("%s 当前下载源: %s\n", core.Info(kind).Name, mirrorLabel(m))
				fmt.Printf("默认地址: %s\n", m.Default)
			}
		}
		printPresetHint(kind)
		return 0
	}

	target := strings.TrimSpace(args[1])
	base := ""
	switch strings.ToLower(target) {
	case "default", "official", "none", "默认", "官方":
		base = ""
	default:
		if preset, found := core.FindMirrorPreset(kind, target); found {
			base = preset.Base
		} else {
			base = target
			if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
				fmt.Fprintf(os.Stderr, "%q 既不是内置镜像，也不是 http(s) 地址\n", target)
				printPresetHint(kind)
				return 2
			}
		}
	}
	cfg.SetMirrorBase(kind, base)
	if err := svc.SetConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if base == "" {
		fmt.Printf("%s 已改回官方源 %s\n", core.Info(kind).Name, core.CanonicalBase(kind))
	} else {
		fmt.Printf("%s 改用 %s\n", core.Info(kind).Name, base)
	}
	return 0
}

// netStatus prints the effective network settings.
func netStatus(svc *core.Service) int {
	cfg := svc.Config()
	fmt.Printf("代理: %s\n", core.ProxyLabel(cfg.Proxy))
	if cfg.Proxy != "" && cfg.Proxy != core.ProxyDirect {
		fmt.Printf("      %s\n", cfg.Proxy)
	}
	fmt.Println()
	fmt.Printf("%s %s %s\n", pad("SDK", 9), pad("下载源", 24), "说明")
	for _, m := range cfg.MirrorSummaries() {
		note := ""
		if m.Base == "" {
			note = "官方"
		} else if m.Preset != "" {
			note = "内置镜像"
		} else {
			note = "自定义"
		}
		shown := m.Base
		if shown == "" {
			shown = m.Default
		}
		fmt.Printf("%s %s %s\n", pad(core.Info(m.Kind).Name, 9), pad(shortURL(shown, 23), 24), note)
	}
	fmt.Println()
	fmt.Println("Java 没有内置镜像：各家的下载接口格式不同，国内镜像也不代理这些 API。")
	fmt.Println("下载慢时给 EnvMgr 一个代理即可覆盖全部来源：envmgr net proxy http://127.0.0.1:7890")
	fmt.Println("改完用 envmgr outdated --refresh 或 envmgr list --remote node 试一次。")
	return 0
}

func mirrorLabel(m core.MirrorSummary) string {
	if m.Base == "" {
		return "官方 " + m.Default
	}
	if m.Preset != "" {
		return m.Base + "（内置镜像）"
	}
	return m.Base + "（自定义）"
}

func printPresetHint(kind core.Kind) {
	presets := core.MirrorPresets(kind)
	if len(presets) == 0 {
		return
	}
	var names []string
	for _, p := range presets {
		names = append(names, p.ID+"="+p.Name)
	}
	fmt.Printf("可选: %s，或直接给一个 http(s) 地址；default 改回官方源\n", strings.Join(names, "、"))
	fmt.Printf("例如: envmgr net %s cn\n", kind)
}

// shortURL keeps a table column readable without hiding which host it is.
func shortURL(raw string, limit int) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit-1] + "…"
}
