package app

import (
	"strings"

	"envmgr/internal/core"

	"github.com/egoist/mygo/ui"
)

// customMirrorLabel is the select entry that reveals the URL field.
const customMirrorLabel = "自定义地址…"

// mirrorKinds are the SDKs with a mirror setting. Java is not here: its vendors
// answer on their own JSON APIs, which no Chinese mirror reproduces, so the
// proxy is the answer there.
var mirrorKinds = []core.Kind{core.Node, core.Python, core.Go, core.Rust}

// loadNetwork fills the settings drafts from the saved configuration.
func (a *App) loadNetwork(cfg core.Config) {
	a.proxyMode, a.proxyURL = proxyFields(cfg.Proxy)
	a.mirrorChoice = map[core.Kind]string{}
	a.mirrorURL = map[core.Kind]*string{}
	for _, kind := range mirrorKinds {
		choice, url := mirrorFields(kind, cfg.MirrorBase(kind))
		a.mirrorChoice[kind] = choice
		a.mirrorURL[kind] = &url
	}
}

// proxyFields turns a stored setting into the segment index and the URL draft.
func proxyFields(raw string) (int, string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "system":
		return 0, ""
	case core.ProxyDirect:
		return 1, ""
	}
	return 2, raw
}

// mirrorFields turns a base URL into the select label and the URL draft.
func mirrorFields(kind core.Kind, base string) (string, string) {
	if base == "" {
		return "官方源", ""
	}
	if preset, ok := core.FindMirrorPreset(kind, base); ok {
		return preset.Name, ""
	}
	return customMirrorLabel, base
}

// proxyFromFields is the value to store for the current drafts.
func (a *App) proxyFromFields() string {
	switch a.proxyMode {
	case 1:
		return core.ProxyDirect
	case 2:
		return strings.TrimSpace(a.proxyURL)
	}
	return ""
}

// mirrorFromFields is the base URL to store for one SDK.
func (a *App) mirrorFromFields(kind core.Kind) string {
	choice := a.mirrorChoice[kind]
	if choice == customMirrorLabel {
		if p := a.mirrorURL[kind]; p != nil {
			return strings.TrimSpace(*p)
		}
		return ""
	}
	for _, p := range core.MirrorPresets(kind) {
		if p.Name == choice {
			return p.Base
		}
	}
	return ""
}

// saveNetwork writes the proxy and mirror drafts, and reports what happened.
func (a *App) saveNetwork() {
	cfg := a.svc.Config()
	proxy, err := core.NormalizeProxy(a.proxyFromFields())
	if err != nil {
		a.pushToast("代理地址无效：" + err.Error())
		return
	}
	cfg.Proxy = proxy
	for _, kind := range mirrorKinds {
		base := a.mirrorFromFields(kind)
		if base != "" && !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			a.pushToast(core.Info(kind).Name + " 的镜像要以 http:// 或 https:// 开头")
			return
		}
		cfg.SetMirrorBase(kind, base)
	}
	if err := a.svc.SetConfig(cfg); err != nil {
		a.pushToast("保存失败：" + err.Error())
		return
	}
	a.loadNetwork(cfg)
	a.pushToast("网络设置已保存")
}

// networkCard is the settings section for downloads: where they come from and
// how they get there.
func (a *App) networkCard(c *ui.Context) {
	t := c.Theme()
	card := a.card(c).Gap(10)
	card.Children(func() {
		ui.Text(c, "下载源与代理").FontSize(13).Bold()
		ui.Text(c, "官方地址在国内经常很慢或连不上。换成本地镜像，或者给 EnvMgr 一个代理，就能正常下载。").
			FontSize(10.5).TextColor(t.TextMuted).Wrap().MaxWidth(560)

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "代理").FontSize(12).Width(120).Shrink(0)
			ui.Segmented(c, &a.proxyMode, "跟随系统", "直连", "自定义").Label("代理")
			if a.proxyMode == 2 {
				ui.TextInput(c, &a.proxyURL).Label("代理地址").
					Placeholder("http://127.0.0.1:7890").Grow(1).MinWidth(0)
			}
		})
		if a.proxyMode == 0 {
			ui.Text(c, "跟随系统：使用 HTTP_PROXY / HTTPS_PROXY 环境变量。").
				FontSize(10).TextColor(t.TextMuted)
		}

		for _, kind := range mirrorKinds {
			kind := kind
			info := core.Info(kind)
			labels := []string{"官方源"}
			for _, p := range core.MirrorPresets(kind) {
				labels = append(labels, p.Name)
			}
			labels = append(labels, customMirrorLabel)
			choice := a.mirrorChoice[kind]
			if choice == "" {
				choice = "官方源"
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, info.Name).FontSize(12).Width(120).Shrink(0)
				if ui.Select(c, &choice, labels).Label(info.Name + " 下载源").Width(260).Changed() {
					a.mirrorChoice[kind] = choice
					if choice == customMirrorLabel && a.mirrorURL[kind] != nil && *a.mirrorURL[kind] == "" {
						*a.mirrorURL[kind] = core.CanonicalBase(kind)
					}
				} else {
					a.mirrorChoice[kind] = choice
				}
				if choice == customMirrorLabel {
					if p := a.mirrorURL[kind]; p != nil {
						ui.TextInput(c, p).Label(info.Name + " 镜像地址").
							Placeholder(core.CanonicalBase(kind)).Grow(1).MinWidth(0)
					}
				}
			})
			if choice != customMirrorLabel && a.mirrorFromFields(kind) == "" {
				ui.Text(c, "    官方 "+core.CanonicalBase(kind)).FontSize(10).TextColor(t.TextMuted).
					SingleLine().Ellipsis("…")
			}
		}
		ui.Text(c, "Java 没有内置镜像：各家的下载接口格式不同。下载慢时用上面的代理，它覆盖所有来源。").
			FontSize(10).TextColor(t.TextMuted).Wrap().MaxWidth(560)

		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "保存网络设置").Clicked() {
				a.saveNetwork()
			}
		})
	})
}
