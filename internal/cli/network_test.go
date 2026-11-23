package cli

import (
	"strings"
	"testing"

	"envmgr/internal/core"
)

func TestNetCommandSwitchesAndResetsMirror(t *testing.T) {
	svc, _ := environmentCLI(t)

	if code := cmdNet(svc, []string{"python", "cn"}); code != 0 {
		t.Fatalf("set mirror exit=%d", code)
	}
	want, _ := core.FindMirrorPreset(core.Python, "cn")
	if got := svc.Config().MirrorBase(core.Python); got != want.Base {
		t.Fatalf("python base = %q, want %q", got, want.Base)
	}

	out := captureCLIOutput(t, func() { cmdNet(svc, []string{"python"}) })
	if !strings.Contains(out, want.Base) {
		t.Fatalf("status output = %q", out)
	}

	if code := cmdNet(svc, []string{"python", "default"}); code != 0 {
		t.Fatalf("reset exit=%d", code)
	}
	if got := svc.Config().MirrorBase(core.Python); got != "" {
		t.Fatalf("python base after reset = %q", got)
	}
}

func TestNetCommandAcceptsCustomURLAndRejectsJunk(t *testing.T) {
	svc, _ := environmentCLI(t)

	if code := cmdNet(svc, []string{"node", "https://mirror.example/node"}); code != 0 {
		t.Fatalf("custom mirror exit=%d", code)
	}
	if got := svc.Config().MirrorBase(core.Node); got != "https://mirror.example/node" {
		t.Fatalf("node base = %q", got)
	}
	if code := cmdNet(svc, []string{"node", "nonsense"}); code != 2 {
		t.Fatalf("junk mirror exit=%d, want 2", code)
	}
	if code := cmdNet(svc, []string{"unknown", "cn"}); code != 2 {
		t.Fatalf("unknown kind exit=%d, want 2", code)
	}
}

func TestNetProxyCommand(t *testing.T) {
	svc, _ := environmentCLI(t)

	if code := cmdNet(svc, []string{"proxy", "127.0.0.1:7890"}); code != 0 {
		t.Fatalf("proxy exit=%d", code)
	}
	if got := svc.Config().Proxy; got != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %q", got)
	}
	out := captureCLIOutput(t, func() { cmdNet(svc, nil) })
	if !strings.Contains(out, "127.0.0.1:7890") {
		t.Fatalf("status output = %q", out)
	}
	if code := cmdNet(svc, []string{"proxy", "direct"}); code != 0 {
		t.Fatalf("direct exit=%d", code)
	}
	if got := svc.Config().Proxy; got != core.ProxyDirect {
		t.Fatalf("proxy = %q", got)
	}
	if code := cmdNet(svc, []string{"proxy"}); code != 2 {
		t.Fatalf("missing argument exit=%d, want 2", code)
	}
}
