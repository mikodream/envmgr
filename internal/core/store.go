package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Installed is one SDK version present on this machine, whether EnvMgr
// downloaded it or found it under another manager.
type Installed struct {
	Kind    Kind              `json:"kind"`
	Version string            `json:"version"`
	Path    string            `json:"path"`
	Manager string            `json:"manager"` // nvm, fnm, pyenv, envmgr, system…
	Env     map[string]string `json:"env,omitempty"`
	// BinDirs are the directories to prepend to PATH, absolute.
	BinDirs []string `json:"binDirs"`
	// Size is the bytes on disk, when known.
	Size int64 `json:"size,omitempty"`
	// FoundAt is when it was last seen (or installed).
	FoundAt time.Time `json:"foundAt"`
}

// ID identifies an install for the UI and for pins: node@20.11.1.
func (i Installed) ID() string { return string(i.Kind) + "@" + i.Version }

// Label is what a version picker shows.
func (i Installed) Label() string {
	if i.Manager == "" || i.Manager == "envmgr" {
		return i.Version
	}
	return i.Version + " · " + i.Manager
}

// Available reports whether the install is still on disk.
func (i Installed) Available() bool {
	_, err := os.Stat(i.Path)
	return err == nil
}

// ExePath returns the absolute path of the SDK's main program.
func (i Installed) ExePath() string {
	info := Info(i.Kind)
	for _, dir := range i.BinDirs {
		p := filepath.Join(dir, ExecName(info.Exe))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ScanInstall fills BinDirs from the kind and the OS, keeping only the dirs
// that exist.
func ScanInstall(inst Installed) Installed {
	info := Info(inst.Kind)
	inst.BinDirs = nil
	for _, rel := range info.BinDirs(runtime.GOOS) {
		dir := JoinDir(inst.Path, rel)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			inst.BinDirs = append(inst.BinDirs, dir)
		}
	}
	if len(inst.BinDirs) == 0 {
		inst.BinDirs = []string{inst.Path}
	}
	inst.Env = map[string]string{}
	if info.HomeVar != "" {
		inst.Env[info.HomeVar] = inst.Path
	}
	if inst.FoundAt.IsZero() {
		inst.FoundAt = time.Now()
	}
	return inst
}

// ProbeVersion learns the version of an SDK at root, best effort: from the
// directory name, a version file, or by asking the program itself.
//
// A directory name is only believed when it looks like a plain version. Go's
// module cache holds directories such as
// toolchain@v0.0.1-go1.27.3.windows-amd64, whose name would otherwise be read
// as version 0.0.1 for a toolchain that is really 1.27.3.
func ProbeVersion(kind Kind, root string) string {
	base := filepath.Base(strings.TrimRight(root, `/\`))
	fromModuleCache := strings.Contains(base, "@") || strings.Contains(filepath.ToSlash(root), "/pkg/mod/")
	// A Go toolchain inside the module cache still knows its version from the
	// VERSION file it ships, so ask for that before treating it as junk.
	if kind == Go {
		if b, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil {
			line := strings.TrimSpace(string(b))
			if strings.HasPrefix(line, "go") {
				return strings.TrimPrefix(strings.Fields(line)[0], "go")
			}
		}
	}
	if !fromModuleCache {
		if v := VersionFromName(base); v != "" {
			return v
		}
	}
	switch kind {
	case Go:
		if b, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil {
			line := strings.TrimSpace(string(b))
			if strings.HasPrefix(line, "go") {
				return strings.TrimPrefix(strings.Fields(line)[0], "go")
			}
		}
	case Java:
		if b, err := os.ReadFile(filepath.Join(root, "release")); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimRight(line, "\r")
				if !strings.HasPrefix(line, "JAVA_VERSION=") {
					continue
				}
				value := strings.TrimPrefix(line, "JAVA_VERSION=")
				// The file writes JAVA_VERSION="17.0.17"; both quotes and the
				// line ending have to go before the value is a version.
				value = strings.Trim(strings.TrimSpace(value), `"`)
				if v := VersionFromName(value); v != "" {
					return v
				}
				return strings.TrimSpace(value)
			}
		}
		// Some JDK builds name the file differently or spell it in the folder
		// name only, which VersionFromName already handled above.
	case Python:
		if b, err := os.ReadFile(filepath.Join(root, "PYTHON_VERSION")); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	// Ask the program, with a short deadline so discovery stays quick.
	exe := Installed{Kind: kind, Path: root}
	if p := ScanInstall(exe).ExePath(); p != "" {
		if out, err := runShort(p, "--version"); err == nil {
			if v := VersionFromOutput(out); v != "" {
				return v
			}
		}
	}
	if fromModuleCache {
		return "" // not an SDK of its own, whatever the program says
	}
	return ""
}

// VersionFromName pulls a version out of a directory name like
// "v20.11.1", "python3.12.4", "node-v20.11.1-linux-x64", "1.22.5".
func VersionFromName(name string) string {
	// Take the longest trailing run that looks like a dotted version.
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < '0' || c > '9' {
			continue
		}
		// Must not be glued to a preceding letter, as in "python312".
		j := i
		dots := 0
		digits := 0
		for j < len(name) {
			c := name[j]
			if c >= '0' && c <= '9' {
				digits++
				j++
				continue
			}
			if c == '.' && j+1 < len(name) && name[j+1] >= '0' && name[j+1] <= '9' {
				dots++
				j++
				continue
			}
			break
		}
		cand := name[i:j]
		if dots > 0 && digits >= 2 {
			return cand
		}
		i = j
	}
	return ""
}

// VersionFromOutput reads a version out of "v20.11.1", "Python 3.12.4",
// "openjdk version \"17.0.2\" 2022-01-18".
func VersionFromOutput(out string) string {
	fields := strings.FieldsFunc(out, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '"' || r == ','
	})
	for _, f := range fields {
		f = strings.TrimLeft(f, "vV")
		if len(f) == 0 || f[0] < '0' || f[0] > '9' {
			continue
		}
		if strings.Count(f, ".") >= 1 {
			if v := VersionFromName(f); v != "" {
				return v
			}
		}
	}
	return ""
}

// Store is EnvMgr's own state: settings, projects, and what discovery saw.
// It lives in one JSON file under the data directory, written atomically.
type Store struct {
	mu       sync.Mutex
	path     string
	Config   Config    `json:"config"`
	Projects []Project `json:"projects"`
	// Manual are versions the user pointed EnvMgr at by hand, for installs
	// that live somewhere discovery does not look.
	Manual []ManualInstall `json:"manual"`
	// GitProfiles bind a name and email to a remote address, for people who
	// commit as different identities to different servers.
	GitProfiles []GitProfile `json:"gitProfiles,omitempty"`
}

// ManualInstall is one SDK location the user added by hand.
type ManualInstall struct {
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Note    string `json:"note,omitempty"`
}

// ManualManager labels versions the user added by hand.
const ManualManager = "手动"

// ManualCopy returns the hand-added entries under the lock.
func (s *Store) ManualCopy() []ManualInstall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ManualInstall{}, s.Manual...)
}

// Config is everything the settings page edits.
type Config struct {
	// DataDir holds downloads and EnvMgr-managed SDKs. Empty means default.
	DataDir string `json:"dataDir"`
	// NodeMirror is a base URL replacing https://nodejs.org/dist.
	NodeMirror string `json:"nodeMirror"`
	// Theme is "system", "light" or "dark".
	Theme string `json:"theme"`
	// Language is "zh" or "en".
	Language string `json:"language"`
	// ShellHook is on once the user installed the opt-in shell integration.
	ShellHook bool `json:"shellHook"`
	// FollowMarkers makes projects without an explicit pin follow .nvmrc etc.
	FollowMarkers bool `json:"followMarkers"`
	// ScanRoots are extra directories to look for projects in.
	ScanRoots []string `json:"scanRoots"`
}

// DefaultConfig is what a fresh install uses.
func DefaultConfig() Config {
	return Config{Theme: "system", Language: "zh", FollowMarkers: true}
}

// OpenStore loads the store, creating it when missing.
func OpenStore() (*Store, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "state.json"), Config: DefaultConfig()}
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, s); err != nil {
			// Keep the broken file, start clean: losing pins must not make
			// the app refuse to open.
			_ = os.Rename(s.path, s.path+".bad")
			s.Config = DefaultConfig()
			s.Projects = nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if s.Config.Theme == "" {
		s.Config = DefaultConfig()
	}
	return s, nil
}

// Path is the store file, for the UI's "reveal" action.
func (s *Store) Path() string { return s.path }

// Save writes the store atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	// Marshal the store itself so a field added later cannot be silently
	// dropped: an explicit field list here once lost the Git bindings.
	b, err := json.MarshalIndent(s.diskState(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// diskState is what gets written: everything the store holds, without the lock
// or the path.
func (s *Store) diskState() any {
	return struct {
		Config      Config          `json:"config"`
		Projects    []Project       `json:"projects"`
		Manual      []ManualInstall `json:"manual"`
		GitProfiles []GitProfile    `json:"gitProfiles,omitempty"`
	}{s.Config, s.Projects, s.Manual, s.GitProfiles}
}

// Update applies fn to the store under the lock and saves.
func (s *Store) Update(fn func(*Store)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
	return s.saveLocked()
}

// ConfigCopy returns a copy of the config for reading.
func (s *Store) ConfigCopy() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Config
}

// GitProfileList returns a copy of the remote-address bindings, sorted by key.
func (s *Store) GitProfileList() []GitProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]GitProfile(nil), s.GitProfiles...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ProjectList returns a copy of the projects, most recently opened first.
func (s *Store) ProjectList() []Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Project(nil), s.Projects...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastOpened.After(out[j].LastOpened)
	})
	return out
}

// ProjectByPath finds a project by its directory.
func (s *Store) ProjectByPath(path string) (Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.Projects {
		if SamePath(p.Path, path) {
			return p, true
		}
	}
	return Project{}, false
}

// ConfigDir is ~/.envmgr (or the OS equivalent).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".envmgr"), nil
}

// DataDirRoot is where SDKs, managers and downloads live: the configured
// directory, or <config>/sdk.
func DataDirRoot(cfg Config) (string, error) {
	if strings.TrimSpace(cfg.DataDir) != "" {
		return expandHome(strings.TrimSpace(cfg.DataDir))
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sdk"), nil
}

// CacheDir is where archives are downloaded before they are unpacked.
func CacheDir(cfg Config) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.DataDir) != "" {
		root, err := DataDirRoot(cfg)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, ".downloads"), nil
	}
	return filepath.Join(dir, "downloads"), nil
}

// expandHome turns a leading ~ into the user's home directory.
func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// SamePath reports whether two paths point at the same place on this OS.
func SamePath(a, b string) bool {
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
	}
	return filepath.Clean(ca) == filepath.Clean(cb)
}
