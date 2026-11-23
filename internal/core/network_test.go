package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRewriteMirrorReplacesOnlyWholePrefix(t *testing.T) {
	rules := []mirrorRule{
		{prefix: "https://nodejs.org/dist", base: "https://npmmirror.com/mirrors/node"},
		{prefix: "https://go.dev/dl", base: "https://golang.google.cn/dl"},
	}
	cases := []struct{ in, want string }{
		{"https://nodejs.org/dist/index.json", "https://npmmirror.com/mirrors/node/index.json"},
		{"https://nodejs.org/dist/v20.11.1/SHASUMS256.txt", "https://npmmirror.com/mirrors/node/v20.11.1/SHASUMS256.txt"},
		{"https://nodejs.org/distance", "https://nodejs.org/distance"},
		{"https://go.dev/dl/?mode=json", "https://golang.google.cn/dl/?mode=json"},
		{"https://go.dev/dlgo", "https://go.dev/dlgo"},
		{"https://github.com/astral-sh/python-build-standalone", "https://github.com/astral-sh/python-build-standalone"},
	}
	for _, tc := range cases {
		if got := rewriteMirror(tc.in, rules); got != tc.want {
			t.Errorf("rewrite %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRewriteMirrorPrefersLongestPrefix(t *testing.T) {
	rules := []mirrorRule{
		{prefix: "https://static.rust-lang.org", base: "https://rsproxy.cn"},
		{prefix: "https://static.rust-lang.org/dist", base: "https://example.test/dist"},
	}
	got := rewriteMirror("https://static.rust-lang.org/dist/channel-rust-stable.toml", rules)
	if want := "https://example.test/dist/channel-rust-stable.toml"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMirrorBaseKeepsLegacyNodeField(t *testing.T) {
	// state.json written by an older version only has nodeMirror; it must keep
	// working instead of silently falling back to nodejs.org.
	cfg := Config{NodeMirror: "https://npmmirror.com/mirrors/node/"}
	if got := cfg.MirrorBase(Node); got != "https://npmmirror.com/mirrors/node" {
		t.Fatalf("legacy base = %q", got)
	}
	cfg.SetMirrorBase(Node, "https://mirror.example/node")
	if cfg.NodeMirror != "" {
		t.Fatal("legacy field kept alongside the new one")
	}
	if got := cfg.MirrorBase(Node); got != "https://mirror.example/node" {
		t.Fatalf("new base = %q", got)
	}
	cfg.SetMirrorBase(Node, "")
	if len(cfg.Mirrors) != 0 {
		t.Fatalf("empty map kept: %v", cfg.Mirrors)
	}
	if got := cfg.MirrorBase(Node); got != "" {
		t.Fatalf("base after clearing = %q", got)
	}
}

func TestNormalizeProxy(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "auto"},
		{"auto", "auto"},
		{"跟随系统", "auto"},
		{"direct", "direct"},
		{"直连", "direct"},
		{"127.0.0.1:7890", "http://127.0.0.1:7890"},
		{"http://127.0.0.1:7890", "http://127.0.0.1:7890"},
		{"socks5://10.0.0.2:1080", "socks5://10.0.0.2:1080"},
	}
	for _, tc := range cases {
		got, err := NormalizeProxy(tc.in)
		if err != nil {
			t.Errorf("NormalizeProxy(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeProxy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := NormalizeProxy("http://"); err == nil {
		t.Fatal("accepted a proxy without a host")
	}
}

func TestMirrorSummariesFlagsCustomAndPreset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetMirrorBase(Go, "https://golang.google.cn/dl")
	cfg.SetMirrorBase(Node, "https://mirror.example/node")
	sums := cfg.MirrorSummaries()
	seen := map[Kind]MirrorSummary{}
	for _, s := range sums {
		seen[s.Kind] = s
	}
	if s := seen[Go]; s.Preset != "cn" || s.Custom {
		t.Fatalf("go summary = %+v", s)
	}
	if s := seen[Node]; s.Preset != "" || !s.Custom {
		t.Fatalf("node summary = %+v", s)
	}
	if s := seen[Python]; s.Base != "" || s.Default == "" {
		t.Fatalf("python summary = %+v", s)
	}
	if _, ok := seen[Java]; ok {
		t.Fatal("Java has no mirror and should not be listed")
	}
}

func TestNodeListGoesThroughMirrorTransport(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `[{"version":"v20.11.1","date":"2024-02-14","lts":"Iron","files":[%q]}]`,
			nodeFeedToken(thisPlatform())+"-tar")
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.SetMirrorBase(Node, server.URL)
	list, err := NewCatalog().List(context.Background(), Node, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Version != "20.11.1" {
		t.Fatalf("list = %+v", list)
	}
	// The archive and the checksum document have to land on the mirror too,
	// otherwise the install downloads from the address that is unreachable.
	rules := cfg.mirrorRules()
	for _, raw := range []string{list[0].URL, list[0].ChecksumURL} {
		if got := rewriteMirror(raw, rules); !strings.HasPrefix(got, server.URL+"/v20.11.1/") {
			t.Fatalf("%q rewrites to %q", raw, got)
		}
	}
	if hits == 0 {
		t.Fatal("mirror never reached")
	}
}

func TestPythonMirrorListReadsChecksums(t *testing.T) {
	triple := Triple()
	sums := strings.Join([]string{
		"aaaa  cpython-3.12.4+20240604-" + triple + "-install_only.tar.gz",
		"bbbb  cpython-3.12.4+20240604-" + triple + "-install_only_stripped.tar.gz",
		"cccc  cpython-3.11.9+20240604-" + triple + "-install_only.tar.gz",
		"dddd  cpython-3.12.4+20240604-x86_64-unknown-linux-gnu-install_only.tar.gz",
	}, "\n")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/LatestRelease/SHA256SUMS" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(sums))
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.SetMirrorBase(Python, server.URL)
	list, err := NewCatalog().List(context.Background(), Python, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Version != "3.12.4" || list[0].SHA256 != "aaaa" {
		t.Fatalf("first = %+v", list[0])
	}
	// A literal + is a 404 on these mirrors; the URL has to encode it.
	if !strings.Contains(list[0].URL, "%2B") || strings.Contains(list[0].URL, "+") {
		t.Fatalf("archive url = %q", list[0].URL)
	}
	if list[0].Note != "20240604" {
		t.Fatalf("note = %q", list[0].Note)
	}
}

func TestPythonMirrorListFallsBackToRoot(t *testing.T) {
	triple := Triple()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/SHA256SUMS" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "eeee  cpython-3.13.1+20250101-%s-install_only.tar.gz\n", triple)
	}))
	defer server.Close()

	list, err := pythonMirrorList(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Version != "3.13.1" {
		t.Fatalf("list = %+v", list)
	}
	if !strings.HasPrefix(list[0].URL, server.URL+"/cpython-") {
		t.Fatalf("url = %q", list[0].URL)
	}
}
