package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// runShort runs a program with a deadline and returns its combined output.
// Discovery asks dozens of programs for their version, so nothing here may
// block for long.
func runShort(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// homeDir is the user's home directory, "" when unknown.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// envOr returns an environment value or a fallback.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// join expands a leading ~ and joins parts.
func join(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	// "~" may stand alone as a part: join("~", "env", "java").
	first, rest := parts[0], parts[1:]
	home := false
	switch {
	case first == "~":
		home, first = true, ""
	case strings.HasPrefix(first, "~/") || strings.HasPrefix(first, `~\`):
		home, first = true, first[2:]
	}
	out := make([]string, 0, len(parts)+1)
	if home {
		out = append(out, homeDir())
	}
	if first != "" {
		out = append(out, first)
	}
	out = append(out, rest...)
	return filepath.Join(out...)
}

// xdgData is $XDG_DATA_HOME or ~/.local/share on non-Windows, %APPDATA% on
// Windows.
func xdgData() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("APPDATA"); v != "" {
			return v
		}
		return join("~", "AppData", "Roaming")
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v
	}
	return join("~", ".local", "share")
}

// appData is where macOS puts application support data.
func appSupport() string {
	return join("~", "Library", "Application Support")
}

// dirExists reports whether p is a directory.
func dirExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// versionDirs lists the subdirectories of p that look like SDK versions and
// returns them as installs rooted at those subdirectories.
func versionDirs(kind Kind, manager, p string) []Installed {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	var out []Installed
	for _, e := range entries {
		if !dirExists(filepath.Join(p, e.Name())) {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Module caches keep third-party code. The one exception is Go's own
		// downloaded toolchains, which are real compilers and worth listing.
		if strings.Contains(name, "@") && !strings.HasPrefix(name, "toolchain@v") {
			continue
		}
		full := filepath.Join(p, name)
		if kind == Java {
			for _, sub := range []string{"Contents/Home", "Home"} {
				if dirExists(filepath.Join(full, filepath.FromSlash(sub))) {
					full = filepath.Join(full, filepath.FromSlash(sub))
					break
				}
			}
		}
		if inst, ok := rootInstall(kind, manager, full, ""); ok {
			out = append(out, inst)
		}
	}
	return out
}

// rootInstall records a directory that is one SDK by itself, when it holds
// the expected program or a version marker.
func rootInstall(kind Kind, manager, root, version string) (Installed, bool) {
	if !dirExists(root) {
		return Installed{}, false
	}
	// Store a stable SDK location rather than a manager's mutable alias.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if version == "" {
		version = ProbeVersion(kind, root)
	}
	if version == "" {
		return Installed{}, false
	}
	inst := ScanInstall(Installed{Kind: kind, Version: version, Path: root, Manager: manager})
	// A root that has no program is not an SDK: skip directories that merely
	// share a name.
	if inst.ExePath() == "" {
		return Installed{}, false
	}
	return inst, true
}

// goToolchainDirs are the module-cache directories where Go keeps toolchains
// it downloaded for itself (GOTOOLCHAIN).
func goToolchainDirs() []string {
	gopath := envOr("GOPATH", "")
	var out []string
	if gopath != "" {
		for _, p := range filepath.SplitList(gopath) {
			out = append(out, filepath.Join(p, "pkg", "mod", "golang.org"))
		}
	}
	out = append(out, join("~", "go", "pkg", "mod", "golang.org"))
	// Keep only directories that exist; duplicates are harmless.
	seen := map[string]bool{}
	kept := out[:0]
	for _, p := range out {
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] || !dirExists(p) {
			continue
		}
		seen[key] = true
		kept = append(kept, p)
	}
	return kept
}

// DiscoverAll finds every SDK version already on this machine.
func DiscoverAll(cfg Config) []Installed {
	var all []Installed
	add := func(list ...Installed) { all = append(all, list...) }
	addDir := func(kind Kind, manager, dir string) {
		if dirExists(dir) {
			add(versionDirs(kind, manager, dir)...)
		}
	}
	addRoot := func(kind Kind, manager, root, version string) {
		if inst, ok := rootInstall(kind, manager, root, version); ok {
			add(inst)
		}
	}
	// addJDK records a JDK, looking one level further down when the named
	// directory is only a wrapper (jdk-25.0.4.101-hotspot/Contents/Home,
	// or an archive that kept its own top folder).
	addJDK := func(kind Kind, manager, dir string) {
		if inst, ok := rootInstall(kind, manager, dir, ""); ok {
			add(inst)
			return
		}
		for _, sub := range []string{"Contents/Home", "Home", "jre"} {
			if inst, ok := rootInstall(kind, manager, filepath.Join(dir, filepath.FromSlash(sub)), ""); ok {
				add(inst)
				return
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if inst, ok := rootInstall(kind, manager, filepath.Join(dir, e.Name()), ""); ok {
				add(inst)
				return
			}
		}
	}
	// addJDKChildren treats every subdirectory of dir as a JDK.
	addJDKChildren := func(kind Kind, manager, dir string) {
		entries, err := os.ReadDir(dir)

		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				addJDK(kind, manager, filepath.Join(dir, e.Name()))
			}
		}
	}

	dataRoot, _ := DataDirRoot(cfg)

	// ---- Node ----
	addDir(Node, "envmgr", filepath.Join(dataRoot, "node"))
	addDir(Node, "nvm", join(envOr("NVM_DIR", "~/.nvm"), "versions", "node"))
	addDir(Node, "nvm", join(envOr("NVM_HOME", ""), ""))
	if runtime.GOOS == "windows" {
		addDir(Node, "nvm", join(xdgData(), "nvm"))
	}
	add(fnmNodeInstalls()...)
	addDir(Node, "n", join("/usr/local/n/versions/node"))
	addDir(Node, "n", join(envOr("N_PREFIX", ""), "n", "versions", "node"))
	addDir(Node, "n", join("~", "n", "versions", "node"))
	addDir(Node, "volta", join("~/.volta/tools/image/node"))
	addDir(Node, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "nodejs"))
	addDir(Node, "mise", join(miseDataDir(), "installs", "node"))

	// ---- Python ----
	addDir(Python, "envmgr", filepath.Join(dataRoot, "python"))
	addDir(Python, "pyenv", join(envOr("PYENV_ROOT", "~/.pyenv"), "versions"))
	addDir(Python, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "python"))
	addDir(Python, "mise", join(miseDataDir(), "installs", "python"))
	addDir(Python, "uv", join("~/.local/share/uv/python"))
	if runtime.GOOS == "windows" {
		addDir(Python, "system", join(xdgData(), "..", "Local", "Programs", "Python"))
		addDir(Python, "system", `C:\Python312`)
		addDir(Python, "system", `C:\Python311`)
	}
	if runtime.GOOS == "darwin" {
		addDir(Python, "system", "/Library/Frameworks/Python.framework/Versions")
	}
	addDir(Python, "system", "/usr/local/python")

	// ---- Java ----
	addDir(Java, "envmgr", filepath.Join(dataRoot, "java"))
	addDir(Java, "jenv", join(envOr("JENV_ROOT", "~/.jenv"), "versions"))
	addDir(Java, "sdkman", join(envOr("SDKMAN_DIR", "~/.sdkman"), "candidates", "java"))
	addDir(Java, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "java"))
	addDir(Java, "mise", join(miseDataDir(), "installs", "java"))
	addDir(Java, "system", "/usr/lib/jvm")
	// A hand-rolled layout that is common on Windows: one directory holding
	// several JDKs named by version, e.g. ~/env/java/17 or ~/env/jdk-17.0.17.
	for _, root := range []string{
		join("~", "env", "java"),
		join("~", ".jdks"),
		join("~", "java"),
		join("~", "jdks"),
	} {
		addDir(Java, "system", root)
		addJDKChildren(Java, "system", root)
	}
	if runtime.GOOS == "darwin" {
		// Both system-wide and user-local JDK bundles are common on macOS.
		for _, root := range []string{"/Library/Java/JavaVirtualMachines", join("~", "Library", "Java", "JavaVirtualMachines")} {
			addDir(Java, "system", root)
			addJDKChildren(Java, "system", root)
		}
	}
	if runtime.GOOS == "windows" {
		for _, base := range []string{`C:\Program Files\Java`, `C:\Program Files\Eclipse Adoptium`, `C:\Program Files\Microsoft`, `C:\Program Files\Zulu`, `C:\Program Files\Amazon Corretto`, `C:\Program Files\BellSoft`} {
			entries, _ := os.ReadDir(base)
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				addJDK(Java, "system", filepath.Join(base, e.Name()))
			}
		}
	}
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		addRoot(Java, "system", jh, "")
	}

	// ---- Go ----
	addDir(Go, "envmgr", filepath.Join(dataRoot, "go"))
	addDir(Go, "sdk", join("~/sdk"))
	for _, p := range []string{"/usr/local/go", "/usr/lib/go", "/opt/go"} {
		addRoot(Go, "system", p, "")
	}
	addDir(Go, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "golang"))
	addRoot(Go, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "golang", "latest", "go"), "")
	addDir(Go, "mise", join(miseDataDir(), "installs", "go"))
	addDir(Go, "system", join("~", "env", "go"))
	addDir(Go, "system", join("~", "go", "sdk"))
	// Go downloads extra toolchains for itself into the module cache when a
	// go.mod asks for a newer version than the local one. Those are real,
	// runnable toolchains, so list them — labelled for what they are.
	for _, cacheRoot := range goToolchainDirs() {
		for _, inst := range versionDirs(Go, "GOTOOLCHAIN", cacheRoot) {
			// The directory name is toolchain@v0.0.1-go1.27.3.windows-amd64;
			// ProbeVersion reads the VERSION file inside it.
			add(inst)
		}
	}
	if runtime.GOOS == "windows" {
		addRoot(Go, "system", `C:\Program Files\Go`, "")
	}
	if gr := os.Getenv("GOROOT"); gr != "" {
		addRoot(Go, "system", gr, "")
	}

	// ---- Rust ----
	for _, home := range []string{filepath.Join(dataRoot, "rustup"), envOr("RUSTUP_HOME", "~/.rustup")} {
		if dirExists(filepath.Join(home, "toolchains")) {
			for _, inst := range versionDirs(Rust, "rustup", filepath.Join(home, "toolchains")) {
				// Toolchain dirs are named 1.79.0-x86_64-… or stable-x86_64-…
				inst.Version = strings.SplitN(filepath.Base(inst.Path), "-", 2)[0]
				add(inst)
			}
		}
	}
	addDir(Rust, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "rust"))
	addDir(Rust, "mise", join(miseDataDir(), "installs", "rust"))

	// ---- Ruby ----
	addDir(Ruby, "envmgr", filepath.Join(dataRoot, "ruby"))
	addDir(Ruby, "rbenv", join(envOr("RBENV_ROOT", "~/.rbenv"), "versions"))
	addDir(Ruby, "rvm", join("~/.rvm/rubies"))
	addDir(Ruby, "asdf", join(envOr("ASDF_DATA_DIR", "~/.asdf"), "installs", "ruby"))
	addDir(Ruby, "mise", join(miseDataDir(), "installs", "ruby"))
	if runtime.GOOS == "windows" {
		addDir(Ruby, "system", `C:\Ruby33-x64`)
		addDir(Ruby, "system", `C:\Ruby32-x64`)
	}
	if runtime.GOOS == "darwin" {
		addRoot(Ruby, "system", "/usr/bin/ruby", "")
	}

	// ---- Whatever is first on PATH ----
	for _, kind := range Kinds {
		if inst, ok := pathInstall(kind); ok {
			add(inst)
		}
	}

	return dedupeInstalls(all)
}

// pathInstall follows the program of a kind found on PATH back to its root.
func pathInstall(kind Kind) (Installed, bool) {
	info := Info(kind)
	for _, name := range exeNames(info.Exe) {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			p = real
		}
		binDir := filepath.Dir(p)
		// Manager shims dispatch to an active version; they are not SDKs.
		if filepath.Base(binDir) == "shims" {
			continue
		}
		root := filepath.Dir(binDir)
		// A version directory directly above bin is the SDK root; otherwise
		// the binary sits in the root itself (Windows layouts).
		if base := filepath.Base(binDir); base != "bin" && base != "Scripts" {
			root = binDir
		}
		// A program without a version it can report is not an install to
		// offer: guessing one would put a wrong version in front of the user.
		version := ProbeVersion(kind, root)
		if version == "" {
			continue
		}
		inst := ScanInstall(Installed{Kind: kind, Version: version, Path: root, Manager: "PATH"})
		if inst.ExePath() != "" {
			return inst, true
		}
	}
	return Installed{}, false
}

// exeNames are the program names to look for, most common first.
func exeNames(base string) []string {
	switch base {
	case "python3":
		if runtime.GOOS == "windows" {
			return []string{"python", "python3"}
		}
		return []string{"python3", "python"}
	case "node":
		return []string{"node"}
	case "java":
		return []string{"java"}
	case "go":
		return []string{"go"}
	case "cargo":
		return []string{"cargo", "rustc"}
	case "ruby":
		return []string{"ruby"}
	}
	return []string{base}
}

// fnmDataDir is fnm's configured or first existing data directory.
func fnmDataDir() string {
	dirs := fnmDataDirs()
	if os.Getenv("FNM_DIR") != "" {
		return dirs[0]
	}
	for _, dir := range dirs {
		if dirExists(dir) {
			return dir
		}
	}
	return dirs[0]
}

// miseDataDir is mise's (formerly rtx) data directory.
func miseDataDir() string {
	if v := os.Getenv("MISE_DATA_DIR"); v != "" {
		return v
	}
	if runtime.GOOS == "darwin" {
		return join(appSupport(), "mise")
	}
	return join(xdgData(), "mise")
}

// dedupeInstalls drops repeats of the same kind, version and path, and sorts
// newest first per kind.
func dedupeInstalls(list []Installed) []Installed {
	seen := map[string]bool{}
	out := make([]Installed, 0, len(list))
	for _, inst := range list {
		path := filepath.Clean(inst.Path)
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		if runtime.GOOS == "windows" {
			path = strings.ToLower(path)
		}
		key := string(inst.Kind) + "|" + inst.Version + "|" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, inst)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindOrder(out[i].Kind) < kindOrder(out[j].Kind)
		}
		return ParseVersion(out[i].Version).Compare(ParseVersion(out[j].Version)) > 0
	})
	return out
}

// kindOrder is the display order of kinds.
func kindOrder(k Kind) int {
	for i, kk := range Kinds {
		if kk == k {
			return i
		}
	}
	return len(Kinds)
}

// InstalledOf returns the installs of one kind from a discovery result.
func InstalledOf(list []Installed, kind Kind) []Installed {
	var out []Installed
	for _, inst := range list {
		if inst.Kind == kind {
			out = append(out, inst)
		}
	}
	return out
}

// Best returns the newest installed version of kind satisfying expr.
func Best(list []Installed, kind Kind, expr string) (Installed, bool) {
	var best Installed
	found := false
	for _, inst := range list {
		if inst.Kind != kind {
			continue
		}
		v := ParseVersion(inst.Version)
		if !Matches(v, expr) {
			continue
		}
		if !found || v.Compare(ParseVersion(best.Version)) > 0 {
			best, found = inst, true
		}
	}
	return best, found
}

// Exact finds the install of kind with exactly this version.
func Exact(list []Installed, kind Kind, version string) (Installed, bool) {
	want := ParseVersion(version)
	for _, inst := range list {
		if inst.Kind != kind {
			continue
		}
		if ParseVersion(inst.Version).Compare(want) == 0 {
			return inst, true
		}
	}
	return Installed{}, false
}

// DirSize sums the bytes under a directory, stopping at the limit so a scan
// of a huge SDK stays cheap.
func DirSize(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.IsDir() {
			total += DirSize(full)
		} else {
			total += info.Size()
		}
	}
	return total
}

// HumanSize formats bytes for the UI.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGT"[exp]) + "B"
}
