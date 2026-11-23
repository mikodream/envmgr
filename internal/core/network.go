package core

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Download speed is the one thing EnvMgr cannot fix in software: nodejs.org,
// go.dev, static.rust-lang.org and github.com are all slow or unreachable from
// mainland China. This file makes the two escape hatches first-class settings:
//
//   - a proxy, applied to every request EnvMgr makes;
//   - a per-SDK mirror base URL, which rewrites the vendor's address before the
//     request goes out, so the version index, the checksum document and the
//     archive all come from the same place.

// ProxyAutomatic follows HTTP_PROXY / HTTPS_PROXY / NO_PROXY, like curl does.
const ProxyAutomatic = ""

// ProxyDirect ignores the environment and connects straight to the origin.
const ProxyDirect = "direct"

// MirrorPreset is one known replacement for a vendor's own servers.
type MirrorPreset struct {
	// ID is the short name used on the command line, such as "cn".
	ID string
	// Name is what the settings page shows.
	Name string
	// Base is the URL that replaces the vendor's base.
	Base string
}

// mirrorPresets holds only mirrors that were tested against the real files:
// the index, a checksum document and an archive all resolve from them.
//
// Java has no preset on purpose. Each vendor exposes its own JSON API with its
// own layout (Adoptium's /v3/assets, Azul's package records, BelSoft's catalog),
// and none of the Chinese mirrors reproduce those APIs. A proxy is the honest
// answer there, and it is what the settings page suggests instead.
var mirrorPresets = map[Kind][]MirrorPreset{
	Node: {
		{ID: "cn", Name: "npmmirror 国内镜像", Base: "https://npmmirror.com/mirrors/node"},
	},
	Python: {
		{ID: "cn", Name: "南京大学镜像（github-release）", Base: "https://mirror.nju.edu.cn/github-release/astral-sh/python-build-standalone"},
	},
	Go: {
		{ID: "cn", Name: "golang.google.cn", Base: "https://golang.google.cn/dl"},
	},
	Rust: {
		{ID: "cn", Name: "rsproxy.cn", Base: "https://rsproxy.cn"},
	},
}

// MirrorPresets lists the built-in mirrors for one SDK.
func MirrorPresets(kind Kind) []MirrorPreset { return mirrorPresets[kind] }

// FindMirrorPreset resolves a preset by id or by exact base URL.
func FindMirrorPreset(kind Kind, id string) (MirrorPreset, bool) {
	id = strings.TrimSpace(id)
	for _, p := range MirrorPresets(kind) {
		if strings.EqualFold(p.ID, id) || p.Base == strings.TrimRight(id, "/") {
			return p, true
		}
	}
	return MirrorPreset{}, false
}

// CanonicalBase is the vendor address a mirror replaces, empty when the SDK has
// more than one address (Java) or no mirror support at all (Ruby).
func CanonicalBase(kind Kind) string {
	switch kind {
	case Node:
		return "https://nodejs.org/dist"
	case Python:
		return "https://github.com/" + pythonRepo
	case Go:
		return "https://go.dev/dl"
	case Rust:
		return "https://static.rust-lang.org"
	}
	return ""
}

// MirrorBase returns the base URL configured for one SDK, without a trailing
// slash. Empty means "use the vendor's own servers".
func (c Config) MirrorBase(kind Kind) string {
	if base := strings.TrimRight(strings.TrimSpace(c.Mirrors[string(kind)]), "/"); base != "" {
		return base
	}
	// state.json files written before mirrors became a map kept the Node base
	// in its own field; keep honouring it so an upgrade does not lose it.
	if kind == Node {
		return strings.TrimRight(strings.TrimSpace(c.NodeMirror), "/")
	}
	return ""
}

// SetMirrorBase stores a base URL, or clears it when base is empty.
func (c *Config) SetMirrorBase(kind Kind, base string) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		delete(c.Mirrors, string(kind))
		if kind == Node {
			c.NodeMirror = ""
		}
		if len(c.Mirrors) == 0 {
			c.Mirrors = nil
		}
		return
	}
	if c.Mirrors == nil {
		c.Mirrors = map[string]string{}
	}
	c.Mirrors[string(kind)] = base
	if kind == Node {
		// One source of truth from here on.
		c.NodeMirror = ""
	}
}

// MirrorSummary is what the CLI and the settings page print for one SDK.
type MirrorSummary struct {
	Kind    Kind
	Base    string // effective base, empty means the vendor default
	Preset  string // preset id when Base matches one
	Custom  bool   // Base is set but matches no preset
	Default string // the vendor address, for display
}

// MirrorSummaries describes the effective source of every SDK that has one.
func (c Config) MirrorSummaries() []MirrorSummary {
	var out []MirrorSummary
	for _, kind := range Kinds {
		def := CanonicalBase(kind)
		if def == "" {
			continue
		}
		base := c.MirrorBase(kind)
		s := MirrorSummary{Kind: kind, Base: base, Default: def, Custom: base != ""}
		if base != "" {
			if p, ok := FindMirrorPreset(kind, base); ok {
				s.Preset, s.Custom = p.ID, false
			}
		}
		out = append(out, s)
	}
	return out
}

// mirrorRule rewrites one vendor prefix to a mirror. Rewriting at the transport
// level means every request an installer makes - index, checksum document,
// archive - is covered without each provider knowing about mirrors.
type mirrorRule struct {
	prefix string
	base   string
}

func (c Config) mirrorRules() []mirrorRule {
	var rules []mirrorRule
	add := func(kind Kind, prefixes ...string) {
		base := c.MirrorBase(kind)
		if base == "" {
			return
		}
		for _, p := range prefixes {
			rules = append(rules, mirrorRule{prefix: p, base: base})
		}
	}
	add(Node, "https://nodejs.org/dist")
	add(Go, "https://go.dev/dl")
	add(Rust, "https://static.rust-lang.org")
	// Python is not a prefix swap: the mirror publishes its own directory
	// layout (see pythonInstaller.List). Its base is used there instead.
	return rules
}

// rewriteMirror replaces the longest matching prefix. The next byte has to be a
// separator, so a mirror never swallows an unrelated host that merely starts
// with the same text.
func rewriteMirror(raw string, rules []mirrorRule) string {
	best := -1
	for i, r := range rules {
		if !strings.HasPrefix(raw, r.prefix) {
			continue
		}
		rest := raw[len(r.prefix):]
		if rest != "" && rest[0] != '/' && rest[0] != '?' {
			continue
		}
		if best < 0 || len(rules[i].prefix) > len(rules[best].prefix) {
			best = i
		}
	}
	if best < 0 {
		return raw
	}
	return rules[best].base + raw[len(rules[best].prefix):]
}

// proxyFunc turns the setting into what net/http wants. A nil function means
// "no proxy", which is not the same as "work it out from the environment".
func proxyFunc(raw string) (func(*http.Request) (*url.URL, error), error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "system":
		return http.ProxyFromEnvironment, nil
	case ProxyDirect, "none", "off", "直连":
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		// A bare host:port is what people paste most often; url.Parse would
		// read its colon as a scheme separator.
		u, err = url.Parse("http://" + strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
	}
	if u.Host == "" {
		return nil, errNoProxyHost
	}
	return http.ProxyURL(u), nil
}

type netError string

func (e netError) Error() string { return string(e) }

const errNoProxyHost = netError("代理地址缺少主机名")

// NormalizeProxy validates a proxy setting and returns the form to store:
// "auto", "direct", or a URL with a scheme and a host.
func NormalizeProxy(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch strings.ToLower(raw) {
	case "", "auto", "system", "跟随系统":
		return "auto", nil
	case ProxyDirect, "none", "off", "直连":
		return ProxyDirect, nil
	}
	// "127.0.0.1:7890" is what people paste most often; url.Parse reads the
	// colon as a scheme separator and rejects it, so add the scheme first.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", errNoProxyHost
	}
	return u.String(), nil
}

// ProxyLabel is what the settings page and `envmgr net` show for a setting.
func ProxyLabel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "system":
		return "跟随系统"
	case ProxyDirect, "none", "off":
		return "直连"
	}
	return raw
}

// mirrorRoundTripper applies the mirror rules to every request, including the
// ones net/http creates while following a redirect.
type mirrorRoundTripper struct {
	base  http.RoundTripper
	rules []mirrorRule
}

func (t *mirrorRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.rules) == 0 || req.URL == nil {
		return t.base.RoundTrip(req)
	}
	if rewritten := rewriteMirror(req.URL.String(), t.rules); rewritten != req.URL.String() {
		u, err := url.Parse(rewritten)
		if err != nil {
			return nil, err
		}
		clone := req.Clone(req.Context())
		clone.URL = u
		clone.Host = u.Host
		req = clone
	}
	return t.base.RoundTrip(req)
}

// netState caches the client built for one network configuration. Rebuilding a
// transport per request would drop the connection pool between the version
// index and the archive that follows it.
type netState struct {
	key        string
	client     *http.Client
	noRedirect *http.Client
}

var (
	netMu     sync.Mutex
	netActive atomic.Pointer[netState]
)

func networkKey(cfg Config) string {
	var b strings.Builder
	b.WriteString(cfg.Proxy)
	for _, r := range cfg.mirrorRules() {
		b.WriteString("|")
		b.WriteString(r.prefix)
		b.WriteString(">")
		b.WriteString(r.base)
	}
	return b.String()
}

// ApplyNetworkConfig installs the proxy and mirror settings for the requests
// that follow. It is idempotent: the client is only rebuilt when the settings
// actually changed.
func ApplyNetworkConfig(cfg Config) {
	key := networkKey(cfg)
	if cur := netActive.Load(); cur != nil && cur.key == key {
		return
	}
	netMu.Lock()
	defer netMu.Unlock()
	if cur := netActive.Load(); cur != nil && cur.key == key {
		return
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	// The environment is the default, but an explicit setting wins.
	if fn, err := proxyFunc(cfg.Proxy); err == nil {
		base.Proxy = fn
	}
	base.DialContext = (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	base.TLSHandshakeTimeout = 20 * time.Second
	base.ResponseHeaderTimeout = 60 * time.Second
	tr := &mirrorRoundTripper{base: base, rules: cfg.mirrorRules()}
	netActive.Store(&netState{
		key:        key,
		client:     &http.Client{Transport: tr, Timeout: 30 * time.Minute},
		noRedirect: &http.Client{Transport: tr, Timeout: 60 * time.Second, CheckRedirect: stopRedirects},
	})
}

func stopRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// httpClientNoRedirect returns a client that stops at the first response, which
// is how stable download links are resolved without fetching a whole archive.
func httpClientNoRedirect() *http.Client {
	if s := netActive.Load(); s != nil {
		return s.noRedirect
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: stopRedirects}
}
