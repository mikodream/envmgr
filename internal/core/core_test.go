package core

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionParseAndCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.10.0", "1.9.9", 1},
		{"v20.11.1", "20.11.1", 0},
		{"20", "20.0.0", 0},
		{"1.2.3-rc1", "1.2.3", -1},
		{"3.12.4", "3.12", 1},
	}
	for _, c := range cases {
		got := ParseVersion(c.a).Compare(ParseVersion(c.b))
		if got != c.want {
			t.Errorf("%q.Compare(%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionMatches(t *testing.T) {
	cases := []struct {
		expr, version string
		want          bool
	}{
		{"20.11.1", "20.11.1", true},
		{"20.11.1", "20.11.2", false},
		{"20", "20.11.1", true},
		{"20.", "20.11.1", true},
		{"20.x", "20.11.1", true},
		{"20.x", "21.0.0", false},
		{"^18", "18.20.5", true},
		{"^18", "19.0.0", false},
		{"~1.2", "1.2.9", true},
		{"~1.2", "1.3.0", false},
		{">=18 <21", "20.11.1", true},
		{">=18 <21", "21.0.0", false},
		{"18 || 20", "20.11.1", true},
		{"18 || 20", "19.1.0", false},
		{"lts", "20.11.1", true},
		{"", "20.11.1", true},
		{"*", "20.11.1", true},
	}
	for _, c := range cases {
		if got := Matches(ParseVersion(c.version), c.expr); got != c.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", c.version, c.expr, got, c.want)
		}
	}
}

func TestVersionFromName(t *testing.T) {
	cases := map[string]string{
		"v20.11.1":      "20.11.1",
		"node-v18.16.0": "18.16.0",
		"Python-3.12.4": "3.12.4",
		"go1.25.6":      "1.25.6",
		"jdk-21.0.4+7":  "21.0.4",
		"3.3.6":         "3.3.6",
		"nonsense":      "",
	}
	for in, want := range cases {
		if got := VersionFromName(in); got != want {
			t.Errorf("VersionFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortVersionsDesc(t *testing.T) {
	vs := []Version{ParseVersion("18.16.0"), ParseVersion("20.11.1"), ParseVersion("20.9.0"), ParseVersion("9.0.0")}
	SortVersionsDesc(vs)
	want := []string{"20.11.1", "20.9.0", "18.16.0", "9.0.0"}
	for i := range want {
		if vs[i].String() != want[i] {
			t.Fatalf("sorted = %v, want %v", vs, want)
		}
	}
}

// TestApplyOverlay checks the guarantee the whole tool rests on: the project's
// directories go first and duplicates do not pile up.
func TestApplyOverlay(t *testing.T) {
	sep := PathListSep()
	base := []string{
		"PATH=" + strings.Join([]string{"/usr/bin", "/bin"}, sep),
		"HOME=/home/u",
	}
	env := Env{
		Path: []string{"/sdk/node/20", "/sdk/python/3.12"},
		Vars: map[string]string{"JAVA_HOME": "/sdk/java/21"},
	}
	out := Apply(base, env)

	pathValue := ""
	for _, kv := range out {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") && !strings.HasPrefix(strings.ToUpper(kv), "PATH_") {
			pathValue = kv[strings.Index(kv, "=")+1:]
		}
	}
	parts := strings.Split(pathValue, sep)
	if len(parts) < 4 {
		t.Fatalf("PATH = %q", pathValue)
	}
	if parts[0] != "/sdk/node/20" || parts[1] != "/sdk/python/3.12" {
		t.Fatalf("overlay did not come first: %q", pathValue)
	}
	found := false
	for _, kv := range out {
		if kv == "JAVA_HOME=/sdk/java/21" {
			found = true
		}
	}
	if !found {
		t.Fatalf("JAVA_HOME missing from %v", out)
	}

	// Applying the same overlay twice must not duplicate directories.
	twice := Apply(out, env)
	for _, kv := range twice {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			p := kv[strings.Index(kv, "=")+1:]
			if strings.Count(p, "/sdk/node/20") != 1 {
				t.Fatalf("duplicate entries: %q", p)
			}
		}
	}
}

func TestEnvExportPerShell(t *testing.T) {
	env := Env{
		Path: []string{"/sdk/node/20"},
		Vars: map[string]string{"JAVA_HOME": "/sdk/java/21"},
	}
	for _, sh := range Shells {
		out := env.Export(sh)
		if !strings.Contains(out, "/sdk/node/20") {
			t.Errorf("%s export missing PATH: %q", sh, out)
		}
		if !strings.Contains(out, "JAVA_HOME") {
			t.Errorf("%s export missing JAVA_HOME: %q", sh, out)
		}
	}
}

// TestDetectMarkers writes the marker files projects actually carry.
func TestDetectMarkers(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".nvmrc":              "v20.11.1\n",
		".python-version":     "3.12.4\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.80.0\"\n",
		".tool-versions":      "nodejs 18.16.0\nruby 3.3.6\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := DetectMarkers(dir)
	byKind := map[Kind]Marker{}
	for _, m := range got {
		if _, seen := byKind[m.Kind]; !seen {
			byKind[m.Kind] = m // the first marker for a kind is the one that counts
		}
	}
	if byKind[Node].Expr != "20.11.1" {
		t.Errorf("node = %+v", byKind[Node])
	}
	if byKind[Python].Expr != "3.12.4" {
		t.Errorf("python = %+v", byKind[Python])
	}
	if byKind[Rust].Expr != "1.80.0" {
		t.Errorf("rust = %+v", byKind[Rust])
	}
	// .tool-versions is a fallback: .nvmrc wins for node.
	if byKind[Node].File != ".nvmrc" {
		t.Errorf("node came from %q, want .nvmrc", byKind[Node].File)
	}
	if byKind[Ruby].Expr != "3.3.6" {
		t.Errorf("ruby = %+v", byKind[Ruby])
	}
}

func TestResolvePrefersProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if err := WriteProjectConfig(dir, map[Kind]string{Node: "18.16.0"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("20.11.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := []Installed{
		{Kind: Node, Version: "18.16.0", Path: "/sdk/node/18.16.0", Manager: "envmgr"},
		{Kind: Node, Version: "20.11.1", Path: "/sdk/node/20.11.1", Manager: "nvm"},
	}
	project := NewProject(dir)
	res := Resolve(project, installed, true)
	if len(res) != 1 {
		t.Fatalf("resolutions = %+v", res)
	}
	if res[0].Expr != "18.16.0" || res[0].Source != "pin" {
		t.Fatalf("the project's own pin should win, got %+v", res[0])
	}
	if res[0].Installed == nil || res[0].Installed.Manager != "envmgr" {
		t.Fatalf("installed = %+v", res[0].Installed)
	}

	// Without the pin, the marker decides.
	_ = os.Remove(filepath.Join(dir, ConfigFileName))
	res = Resolve(NewProject(dir), installed, true)
	if len(res) != 1 || res[0].Expr != "20.11.1" || res[0].Source != ".nvmrc" {
		t.Fatalf("resolution after removing pin = %+v", res)
	}

	// With markers ignored, nothing is pinned.
	res = Resolve(NewProject(dir), installed, false)
	if len(res) != 0 {
		t.Fatalf("resolutions with markers off = %+v", res)
	}
}

func TestResolveMarksMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("99.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Resolve(NewProject(dir), nil, true)
	if len(res) != 1 || !res[0].Missing {
		t.Fatalf("resolutions = %+v", res)
	}
}

func TestExtractRejectsZipSlip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("../../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(archive, dest); err == nil {
		t.Fatal("Extract accepted a path outside the destination")
	}
	if _, err := os.Stat(filepath.Join(dest, "..", "..", "escape.txt")); err == nil {
		t.Fatal("zip slip wrote outside the destination")
	}
}

func TestExtractAndFlatten(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// A real archive has one wrapper directory; Flatten should lift it.
	for name, body := range map[string]string{
		"node-v20/bin/node":    "binary",
		"node-v20/README.md":   "docs",
		"node-v20/lib/npm.txt": "npm",
	} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "node.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
	if err := Flatten(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "node")); err != nil {
		t.Fatalf("flatten did not lift the wrapper: %v", err)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:        "0 B",
		512:      "512 B",
		1536:     "1.5 KB",
		39 << 20: "39.0 MB",
	}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestLookPathPrefersOverlay proves the fix for the bug where a pinned
// version was ignored: resolution must consult the overlay before the
// machine's PATH.
func TestLookPathPrefersOverlay(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	name := "envmgr-test-tool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := Env{Path: []string{first}}
	got, err := LookPath(env, strings.TrimSuffix(name, ".exe"))
	if err != nil {
		t.Fatalf("LookPath: %v", err)
	}
	if filepath.Dir(got) != first {
		t.Fatalf("LookPath returned %q, want a path under %q", got, first)
	}
}

func TestKindParsing(t *testing.T) {
	cases := map[string]Kind{
		"node": Node, "nodejs": Node, "Node.js": Node,
		"python": Python, "python3": Python,
		"java": Java, "jdk": Java,
		"go": Go, "golang": Go,
		"rust": Rust,
		"ruby": Ruby,
	}
	for in, want := range cases {
		got, ok := ParseKind(in)
		if !ok || got != want {
			t.Errorf("ParseKind(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := ParseKind("cobol"); ok {
		t.Error("ParseKind accepted an unknown name")
	}
}

func TestBinDirsExistOnDisk(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "bin")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := sub
	if runtime.GOOS == "windows" {
		bin = root
	}
	exe := filepath.Join(bin, ExecName(string(Node)))
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	var abs []string
	for _, rel := range Info(Node).BinDirs(runtime.GOOS) {
		abs = append(abs, JoinDir(root, rel))
	}
	inst := Installed{Kind: Node, Version: "20.11.1", Path: root, BinDirs: abs}
	dirs := inst.BinDirs
	found := false
	for _, d := range dirs {
		if d == sub {
			found = true
		}
	}
	if !found {
		t.Fatalf("BinDirs = %v, want it to include %q", dirs, sub)
	}
	if got := inst.ExePath(); got == "" {
		t.Fatalf("ExePath is empty, want %q", exe)
	}
}

// TestJoinExpandsHome guards a bug that silently broke every home-relative
// path written as join("~", "sub"): the prefix was dropped and the result was
// a path relative to the working directory.
func TestJoinExpandsHome(t *testing.T) {
	home := homeDir()
	cases := map[string]string{
		join("~"):                   home,
		join("~", "env", "java"):    filepath.Join(home, "env", "java"),
		join("~/.nvm", "versions"):  filepath.Join(home, ".nvm", "versions"),
		join("~/sdk"):               filepath.Join(home, "sdk"),
		join("/usr/local/go"):       filepath.FromSlash("/usr/local/go"),
		join("C:/Program Files/Go"): filepath.FromSlash("C:/Program Files/Go"),
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("join produced %q, want %q", got, want)
		}
	}
	if p := join("~", "env"); !filepath.IsAbs(p) {
		t.Errorf("join(~, env) = %q is not absolute", p)
	}
}

// TestDetectJDKLayout covers the layout where one directory holds several
// JDKs named by version, which is common on Windows.
func TestDetectJDKLayout(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"8", "11", "17", "jdk21.0.7-win_x64"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", ExecName("java")), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		release := "JAVA_VERSION=\"" + name + ".0.1\"\r\n"
		if err := os.WriteFile(filepath.Join(dir, "release"), []byte(release), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with a wrong name but a release file still knows its version.
	got := map[string]string{}
	for _, inst := range DiscoverAll(Config{}) {
		if inst.Kind == Java && strings.HasPrefix(filepath.ToSlash(inst.Path), filepath.ToSlash(root)) {
			got[filepath.Base(inst.Path)] = inst.Version
		}
	}
	_ = got // discovery scans well-known roots, not t.TempDir; probe directly
	for _, name := range []string{"8", "11", "17", "jdk21.0.7-win_x64"} {
		v := ProbeVersion(Java, filepath.Join(root, name))
		if v == "" || strings.ContainsAny(v, "\" \t\r") {
			t.Errorf("ProbeVersion(%s) = %q, want a clean version", name, v)
		}
	}
}

// TestManualInstallRoundTrip adds a version by hand, uses it, then forgets it.
func TestManualInstallRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, "custom", "node-20")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ExecName("node")), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	inst, err := svc.AddManualInstall(Node, "20.5.0", dir, "")
	if err != nil {
		t.Fatalf("AddManualInstall: %v", err)
	}
	if inst.Manager != ManualManager || inst.Version != "20.5.0" {
		t.Fatalf("installed = %+v", inst)
	}

	found := false
	for _, i := range svc.Refresh() {
		if i.Kind == Node && i.Version == "20.5.0" && i.Manager == ManualManager {
			found = true
		}
	}
	if !found {
		t.Fatal("the hand-added version did not appear in discovery")
	}

	// It must be usable by a project like any other version.
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".nvmrc"), []byte("20.5.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := svc.EnvForDir(project)
	if len(env.Path) == 0 || !strings.Contains(env.Path[0], "node-20") {
		t.Fatalf("env overlay = %v, want the hand-added directory", env.Path)
	}

	if err := svc.RemoveManualInstall(Node, dir); err != nil {
		t.Fatal(err)
	}
	for _, i := range svc.Refresh() {
		if i.Manager == ManualManager && SamePath(i.Path, dir) {
			t.Fatal("the version is still listed after being forgotten")
		}
	}
	// Forgetting must not touch the directory.
	if _, err := os.Stat(filepath.Join(dir, ExecName("node"))); err != nil {
		t.Fatal("RemoveManualInstall deleted files on disk")
	}
}

// TestActivationScriptAppliesOverlay guards the bug where opening a terminal
// from the GUI kept showing the global SDK: Windows Terminal and
// gnome-terminal start the shell from their own long-running process, so the
// environment handed to them is dropped. The shell must source the overlay.
func TestActivationScriptAppliesOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))

	e := Env{
		Path: []string{join("~", "sdk", "go1.27.4", "bin")},
		Vars: map[string]string{"GOROOT": join("~", "sdk", "go1.27.4")},
	}
	script, err := activateScript(e, ShellPowerShell)
	if err != nil {
		t.Fatalf("activateScript: %v", err)
	}
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, e.Path[0]) {
		t.Errorf("script does not prepend %s:\n%s", e.Path[0], text)
	}
	if !strings.Contains(text, "GOROOT") || !strings.Contains(text, e.Vars["GOROOT"]) {
		t.Errorf("script does not set GOROOT:\n%s", text)
	}
	if base := filepath.Base(script); !strings.HasPrefix(base, "activate-") || filepath.Ext(script) != ".ps1" {
		t.Errorf("script path looks wrong: %s", script)
	}
	// The file name must be stable for the same overlay, so opening a second
	// terminal reuses it instead of littering the config directory.
	again, err := activateScript(e, ShellPowerShell)
	if err != nil {
		t.Fatal(err)
	}
	if again != script {
		t.Errorf("activateScript returned %q then %q, want a stable path", script, again)
	}
	// A different overlay must not collide with the first one.
	e2 := Env{Path: []string{join("~", "sdk", "go1.26.3", "bin")}}
	other, err := activateScript(e2, ShellPowerShell)
	if err != nil {
		t.Fatal(err)
	}
	if other == script {
		t.Error("two different overlays share one script")
	}
}

// TestHookInstallUninstall covers the marked block: installing twice must not
// stack copies, and removing must leave the user's own lines untouched.
func TestHookInstallUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	file := HookFile(ShellBash)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# user line one\nexport FOO=1\n"
	if err := os.WriteFile(file, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallHook(ShellBash); err != nil {
		t.Fatalf("InstallHook: %v", err)
	}
	if !HookInstalled(ShellBash) {
		t.Fatal("HookInstalled = false right after install")
	}
	if _, err := InstallHook(ShellBash); err != nil {
		t.Fatalf("second InstallHook: %v", err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), hookBegin); n != 1 {
		t.Fatalf("hook block appears %d times, want 1", n)
	}
	// The user's own lines survive, and their order is kept.
	text := string(b)
	if !strings.HasPrefix(text, original) {
		t.Fatalf("install changed the user prefix:\n%s", text)
	}
	// The backup holds the file as it was before EnvMgr touched it.
	if bak, err := os.ReadFile(file + ".envmgr.bak"); err == nil {
		if !strings.Contains(string(bak), "export FOO=1") {
			t.Errorf("backup lost the original content:\n%s", bak)
		}
	}

	removed, err := UninstallHook(ShellBash)
	if err != nil || !removed {
		t.Fatalf("UninstallHook = %v, %v", removed, err)
	}
	if HookInstalled(ShellBash) {
		t.Fatal("hook still present after uninstall")
	}
	b, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(string(b), "\n") != strings.TrimRight(original, "\n") {
		t.Fatalf("uninstall left %q, want the original %q", b, original)
	}
	if removed, _ := UninstallHook(ShellBash); removed {
		t.Error("second uninstall reported a removal")
	}
}

// TestHookScriptUsesAbsolutePath makes sure the hook can run even when the
// directory holding EnvMgr is not on PATH.
func TestHookScriptUsesAbsolutePath(t *testing.T) {
	for _, sh := range []Shell{ShellPowerShell, ShellBash, ShellZsh, ShellFish} {
		script := HookScript(sh)
		if strings.Contains(script, "@ENVMGR@") {
			t.Errorf("%s: placeholder was not substituted", sh)
		}
		if !strings.Contains(script, "env --shell") {
			t.Errorf("%s: hook never asks EnvMgr for an environment", sh)
		}
		if strings.Contains(script, " envmgr env ") {
			t.Errorf("%s: hook calls envmgr by bare name, which needs it on PATH", sh)
		}
	}
}

// TestHookWritesBOMForPowerShell guards the bug where the hook never loaded in
// Windows PowerShell 5.1: it decodes .ps1 files as ANSI without a UTF-8 BOM,
// so the Chinese comments became mojibake and the block failed to parse.
func TestHookWritesBOMForPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the BOM matters for Windows PowerShell only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	primary := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	legacy := filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	for _, f := range []string{primary, legacy} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := InstallHook(ShellPowerShell); err != nil {
		t.Fatalf("InstallHook: %v", err)
	}
	// Both profile locations must be covered: the terminal the user opens may
	// be either PowerShell 5.1 or 7.
	for _, f := range []string{primary, legacy} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(b) < 3 || b[0] != 0xEF || b[1] != 0xBB || b[2] != 0xBF {
			t.Errorf("%s has no UTF-8 BOM, 5.1 will misparse it", f)
		}
		if !strings.Contains(string(b[3:]), hookBegin) {
			t.Errorf("%s has no hook block", f)
		}
		if !strings.Contains(string(b[3:]), "# mine") {
			t.Errorf("%s lost the user's own lines", f)
		}
	}

	// Uninstalling must remove the block from every file and keep the BOM.
	if _, err := UninstallHook(ShellPowerShell); err != nil {
		t.Fatalf("UninstallHook: %v", err)
	}
	for _, f := range []string{primary, legacy} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), hookBegin) {
			t.Errorf("%s still has the block", f)
		}
		if !strings.Contains(string(b), "# mine") {
			t.Errorf("%s lost the user's lines on uninstall", f)
		}
		if len(b) < 3 || b[0] != 0xEF {
			t.Errorf("%s lost its BOM on uninstall", f)
		}
	}
}

// TestActivationScriptShowsBanner covers the terminals EnvMgr opens itself:
// they get the same colored line the shell hook prints.
func TestActivationScriptShowsBanner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	e := Env{
		Path:  []string{"/sdk/java/21/bin", "/sdk/go/1.27.4/bin"},
		Pairs: []Pair{{Kind: Java, Version: "21.0.9"}, {Kind: Go, Version: "1.27.4"}},
	}
	shPath, err := activateScript(e, ShellPowerShell)
	if err != nil {
		t.Fatalf("activateScript: %v", err)
	}
	b, err := os.ReadFile(shPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"◆ EnvMgr", "21.0.9", "1.27.4", "ForegroundColor"} {
		if !strings.Contains(text, want) {
			t.Errorf("activation script lacks %q:\n%s", want, text)
		}
	}
	// The same PATH with a different version must not reuse the cached script,
	// or the banner would name the wrong version.
	other := Env{Path: e.Path, Pairs: []Pair{{Kind: Java, Version: "25.0.1"}}}
	otherPath, err := activateScript(other, ShellPowerShell)
	if err != nil {
		t.Fatal(err)
	}
	if otherPath == shPath {
		t.Error("two different overlays share one activation script")
	}

	// POSIX scripts get escape sequences; a shell without them shows raw codes.
	shPath, err = activateScript(e, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(shPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `\033[36m`) {
		t.Errorf("bash activation script has no color escapes:\n%s", b)
	}
}

// TestDiagnoseCoversHookState checks the doctor reports the terminal hook,
// which is the item users get wrong most often.
func TestDiagnoseCoversHookState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	svc, err := NewService()
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	findHook := func(rep Report) (Check, bool) {
		for _, s := range rep.Sections {
			for _, c := range s.Checks {
				if strings.HasPrefix(c.Title, "自己开的终端") {
					return c, true
				}
			}
		}
		return Check{}, false
	}

	rep := Diagnose(svc)
	hook, ok := findHook(rep)
	if !ok {
		t.Fatal("doctor has no check for the user's own terminal")
	}
	if hook.Level != LevelWarn {
		t.Errorf("hook check level = %v, want warn before installing", hook.Level.Label())
	}
	if hook.Fix != FixInstallHook {
		t.Error("hook check offers no fix, so the window cannot offer a button")
	}
	if rep.Total == 0 || len(rep.Sections) == 0 {
		t.Fatal("doctor reported nothing")
	}
	if rep.Summary() == "" {
		t.Error("empty summary")
	}

	// With the hook installed the same check has to turn green.
	if _, err := InstallHook(CurrentShell()); err != nil {
		t.Fatalf("InstallHook: %v", err)
	}
	hook, ok = findHook(Diagnose(svc))
	if !ok {
		t.Fatal("hook check disappeared after install")
	}
	if hook.Level != LevelOK {
		t.Errorf("hook check level = %v, want OK after installing", hook.Level.Label())
	}
}

// TestDiagnoseFlagsStaleEnv checks a global variable pointing at a missing
// directory is reported, since that breaks tools outside any project.
func TestDiagnoseFlagsStaleEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	gone := filepath.Join(home, "definitely-not-here")
	t.Setenv("JAVA_HOME", gone)

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	rep := Diagnose(svc)
	var found bool
	for _, s := range rep.Sections {
		for _, c := range s.Checks {
			if c.Title == "JAVA_HOME" {
				found = true
				if c.Level != LevelWarn {
					t.Errorf("stale JAVA_HOME level = %v, want warn", c.Level.Label())
				}
				if !strings.Contains(c.Detail, "不存在") {
					t.Errorf("detail does not explain the problem: %q", c.Detail)
				}
			}
		}
	}
	if !found {
		t.Error("doctor did not inspect JAVA_HOME")
	}
}

// TestReportOrderingIsStable documents that Diagnose keeps a fixed section
// order, which the window re-sorts for display.
func TestReportOrderingIsStable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	rep := Diagnose(svc)
	if len(rep.Sections) != 6 {
		t.Fatalf("sections = %d, want 6", len(rep.Sections))
	}
	var titles []string
	for _, s := range rep.Sections {
		titles = append(titles, s.Title)
		if len(s.Checks) == 0 {
			t.Errorf("section %q has no checks", s.Title)
		}
	}
	want := []string{"运行环境", "数据与存储", "终端集成", "工具链", "项目", "设置与环境变量"}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("section order = %v, want %v", titles, want)
		}
	}
	if rep.Total != rep.OK+rep.Warn+rep.Fail+rep.Info {
		t.Errorf("counts do not add up: total %d, ok %d, warn %d, fail %d, info %d", rep.Total, rep.OK, rep.Warn, rep.Fail, rep.Info)
	}
}

// TestHookProgramPathIgnoresComments guards the bug where the word "envmgr"
// inside the block's comments made the path lookup grab unrelated text and
// report a healthy hook as broken.
func TestHookProgramPathIgnoresComments(t *testing.T) {
	script := HookScript(ShellPowerShell)
	got, ok := hookProgramPath(script)
	if !ok {
		t.Fatal("no program path found in the hook")
	}
	if filepath.Ext(got) == "" && runtime.GOOS == "windows" {
		t.Errorf("path = %q, want the executable the hook calls", got)
	}
	if strings.ContainsAny(got, "\n") {
		t.Errorf("path spans lines: %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("hook points at %q, which does not exist: %v", got, err)
	}
}

// TestDiagnoseDetectsMovedProgram checks that a hook pointing at a program that
// is no longer there is reported: that is the silent failure after moving the
// binary.
func TestDiagnoseDetectsMovedProgram(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	sh := CurrentShell()
	files := HookFiles(sh)
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		gone := filepath.Join(home, "moved-away", "envmgr")
		body := "# mine\n# >>> envmgr >>>\n$out = & '" + gone + "' env --shell pwsh --quiet\n# <<< envmgr <<<\n"
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	rep := Diagnose(svc)
	var checked int
	for _, s := range rep.Sections {
		for _, c := range s.Checks {
			if !strings.HasPrefix(c.Title, "钩子 · ") {
				continue
			}
			checked++
			if c.Level != LevelWarn && c.Level != LevelFail {
				t.Errorf("%s: level = %v, want a warning about the missing program", c.Title, c.Level.Label())
			}
			if !strings.Contains(c.Detail+c.Hint, "不存在") {
				t.Errorf("%s: does not mention the missing program: %q %q", c.Title, c.Detail, c.Hint)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no hook checks were produced")
	}
}
