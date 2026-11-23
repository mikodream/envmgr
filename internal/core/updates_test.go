package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLineOfUsesTheEpochEachSDKVersionsBy(t *testing.T) {
	cases := []struct {
		kind    Kind
		version string
		want    string
	}{
		{Node, "20.11.1", "20"},
		{Java, "21.0.9", "21"},
		{Rust, "1.100.0", "1"},
		{Python, "3.12.4", "3.12"},
		{Go, "1.27.4", "1.27"},
	}
	for _, c := range cases {
		if got := LineOf(c.kind, ParseVersion(c.version)); got != c.want {
			t.Errorf("LineOf(%s %s) = %q, want %q", c.kind, c.version, got, c.want)
		}
	}
}

// The schedule publishes the line exactly as it prefixes versions, so the
// longest matching prefix is the right record.
func TestCycleForPicksTheLongestMatchingPrefix(t *testing.T) {
	cycles := []Cycle{
		{Cycle: "3", EOL: "2030-01-01"},
		{Cycle: "3.12", EOL: "2029-10-31"},
		{Cycle: "3.13", EOL: "2029-10-31"},
	}
	c, ok := CycleFor(ParseVersion("3.12.4"), cycles)
	if !ok || c.Cycle != "3.12" {
		t.Fatalf("cycle = %+v, found=%v", c, ok)
	}
	// A longer cycle must not match a shorter version.
	if _, ok := CycleFor(ParseVersion("3.1.0"), cycles); !ok {
		// "3" still matches, which is the intended fallback.
		t.Fatal("prefix fallback lost")
	}
}

func TestEOLStateReadsThePublishedDate(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		eol   string
		open  bool
		state EOLState
	}{
		{"2026-01-01", false, EOLEnded},
		{"2026-12-01", false, EOLSoon},
		{"2028-01-01", false, EOLActive},
		{"", true, EOLActive},
		{"", false, EOLUnknown},
	}
	for _, c := range cases {
		state, _ := EOLStateFor(Cycle{EOL: c.eol, Open: c.open}, now)
		if state != c.state {
			t.Errorf("eol=%q open=%v → %v, want %v", c.eol, c.open, state, c.state)
		}
	}
}

func TestBuildUpdatesComparesEachVersionWithItsOwnLine(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	installed := []Installed{
		{Kind: Node, Version: "20.11.1", Path: "/n20", Manager: "envmgr"},
		{Kind: Python, Version: "3.12.4", Path: "/p312", Manager: "uv"},
	}
	remotes := map[Kind][]Remote{
		Node: {
			{Kind: Node, Version: "22.1.0"},
			{Kind: Node, Version: "20.19.0"},
			{Kind: Node, Version: "20.20.0-rc.1"}, // prereleases never win
		},
		Python: {
			{Kind: Python, Version: "3.13.1"},
			{Kind: Python, Version: "3.12.7"},
		},
	}
	cycles := map[Kind][]Cycle{
		Node:   {{Cycle: "20", EOL: "2026-04-30"}},
		Python: {{Cycle: "3.12", EOL: "2028-10-31"}},
	}
	got := buildUpdates(installed, remotes, cycles, now)
	if len(got) != 2 {
		t.Fatalf("updates = %+v", got)
	}

	// End-of-life sorts first.
	if got[0].Kind != Node || got[0].EOLState != EOLEnded {
		t.Fatalf("first update = %+v", got[0])
	}
	if got[0].Latest != "20.19.0" {
		t.Errorf("Latest = %q, want 20.19.0 (same line only)", got[0].Latest)
	}
	if got[0].Level != MinorUpdate {
		t.Errorf("Level = %v, want minor", got[0].Level)
	}
	if !got[0].HasNewerLine() || got[0].Newest != "22.1.0" {
		t.Errorf("newer line = %q, want 22.1.0", got[0].Newest)
	}

	if got[1].Kind != Python || got[1].Latest != "3.12.7" || got[1].Level != PatchUpdate {
		t.Fatalf("python update = %+v", got[1])
	}
	if got[1].EOLState != EOLActive {
		t.Errorf("python eol state = %v", got[1].EOLState)
	}
}

// A version whose line the source no longer lists still reports its EOL date
// instead of disappearing from the answer.
func TestBuildUpdatesKeepsEOLWithoutARemoteList(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	installed := []Installed{{Kind: Ruby, Version: "2.6.10", Manager: "PATH"}}
	got := buildUpdates(installed, nil, map[Kind][]Cycle{
		Ruby: {{Cycle: "2.6", EOL: "2022-03-31"}},
	}, now)
	if len(got) != 1 {
		t.Fatalf("updates = %+v", got)
	}
	if got[0].EOLState != EOLEnded || got[0].EOL != "2022-03-31" {
		t.Fatalf("ruby update = %+v", got[0])
	}
	if got[0].Outdated() {
		t.Fatal("a nil remote list must not look like an update")
	}
}

func TestParseLifecycleHandlesFalseDatesAndLTSFlag(t *testing.T) {
	body := []byte(`[
		{"cycle":"26","eol":"2029-04-30","lts":"2026-10-28","latest":"26.13.1"},
		{"cycle":"1.27","eol":false,"latest":"1.27.4"}
	]`)
	cycles, err := parseLifecycle(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cycles) != 2 {
		t.Fatalf("cycles = %+v", cycles)
	}
	if !cycles[0].LTS || cycles[0].EOL != "2029-04-30" {
		t.Errorf("first cycle = %+v", cycles[0])
	}
	if !cycles[1].Open || cycles[1].EOL != "" {
		t.Errorf("second cycle = %+v", cycles[1])
	}
	state, _ := EOLStateFor(cycles[1], time.Now())
	if state != EOLActive {
		t.Errorf("open cycle state = %v, want active", state)
	}
}

func TestLineTargetStaysInsideTheLine(t *testing.T) {
	list := []Remote{
		{Kind: Node, Version: "22.1.0"},
		{Kind: Node, Version: "20.19.0"},
		{Kind: Node, Version: "20.21.0-rc.1"},
	}
	got, ok := lineTarget(list, Node, ParseVersion("20.11.1"))
	if !ok || got.Version != "20.19.0" {
		t.Fatalf("target = %+v, found=%v", got, ok)
	}
	if _, ok := lineTarget([]Remote{{Kind: Node, Version: "22.1.0"}}, Node, ParseVersion("20.11.1")); ok {
		t.Fatal("a target outside the line was offered")
	}
}

// Rust publishes channel names and puts the number in the note.
func TestLineTargetReadsRustChannelVersions(t *testing.T) {
	list := []Remote{
		{Kind: Rust, Version: "stable", Note: "1.100.0"},
		{Kind: Rust, Version: "beta", Note: "1.101.0-beta.1"},
	}
	got, ok := lineTarget(list, Rust, ParseVersion("1.96.0"))
	if !ok || remoteVersion(got).String() != "1.100.0" {
		t.Fatalf("target = %+v, found=%v", got, ok)
	}
}

// RepinExact rewrites only the pins that name the old version exactly.
func TestRepinExactLeavesPrefixesAlone(t *testing.T) {
	svc, home := defaultsFixture(t, "20.11.1", "20.19.0")
	exactDir := filepath.Join(home, "exact")
	prefixDir := filepath.Join(home, "prefix")
	for _, dir := range []string{exactDir, prefixDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddProject(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteProjectConfig(exactDir, map[Kind]string{Node: "20.11.1"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectConfig(prefixDir, map[Kind]string{Node: "20"}); err != nil {
		t.Fatal(err)
	}

	old, _ := Exact(svc.Installed(), Node, "20.11.1")
	now, _ := Exact(svc.Installed(), Node, "20.19.0")
	changed, err := svc.RepinExact(old, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Path != exactDir {
		t.Fatalf("changed = %+v", changed)
	}
	pins, err := ReadProjectPins(exactDir)
	if err != nil {
		t.Fatal(err)
	}
	if pins[Node] != "20.19.0" {
		t.Fatalf("exact pin = %q", pins[Node])
	}
	pins, err = ReadProjectPins(prefixDir)
	if err != nil {
		t.Fatal(err)
	}
	if pins[Node] != "20" {
		t.Fatalf("prefix pin was rewritten to %q", pins[Node])
	}
}
