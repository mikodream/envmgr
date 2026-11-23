package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
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

// ProbeVersion learns the version from SDK metadata or its executable before
// falling back to the directory name, which may be a manager alias.
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
	switch kind {
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
					if value[0] >= '0' && value[0] <= '9' {
						return normalizeJavaVersion(value)
					}
					return v
				}
				return strings.TrimSpace(value)
			}
		}
	case Python:
		if b, err := os.ReadFile(filepath.Join(root, "PYTHON_VERSION")); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	// Ask the program, with a short deadline so discovery stays quick.
	exe := Installed{Kind: kind, Path: root}
	if p := ScanInstall(exe).ExePath(); p != "" {
		arg := "--version"
		if kind == Java {
			arg = "-version" // also supported by Java 8
		}
		if out, err := runShort(p, arg); err == nil {
			if kind == Java {
				for _, field := range strings.Fields(out) {
					value := strings.Trim(field, `"`)
					if v := VersionFromName(value); v != "" && value[0] >= '0' && value[0] <= '9' {
						return normalizeJavaVersion(value)
					}
				}
			} else if v := VersionFromOutput(out); v != "" {
				return v
			}
		}
	}
	if fromModuleCache {
		return "" // module-cache names do not describe SDK versions
	}
	return VersionFromName(base)
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
	baseline *storeState // last committed snapshot, for callers using Save
	readErr  error       // latest reload error; the last committed snapshot remains available
	Config   Config      `json:"config"`
	Projects []Project   `json:"projects"`
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
	_ = s.reloadLocked()
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
	// Defaults is the version each SDK falls back to where a directory says
	// nothing, keyed by kind. Without it EnvMgr is silent outside projects.
	Defaults map[string]string `json:"defaults,omitempty"`
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
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path is the store file, for the UI's "reveal" action.
func (s *Store) Path() string { return s.path }

// Save merges edits since this instance's last snapshot into the latest disk
// state. Prefer Update for read-modify-write operations.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	local := s.diskState()
	base := s.baseline
	return s.transactionLocked(func(st *Store) {
		if base == nil {
			st.installState(local)
			return
		}
		mergeConfig := reflect.ValueOf(&st.Config).Elem()
		before, after := reflect.ValueOf(base.Config), reflect.ValueOf(local.Config)
		for i := 0; i < before.NumField(); i++ {
			if !reflect.DeepEqual(before.Field(i).Interface(), after.Field(i).Interface()) {
				mergeConfig.Field(i).Set(after.Field(i))
			}
		}
		st.Projects = mergeEntries(st.Projects, base.Projects, local.Projects, func(p Project) string { return p.ID })
		st.Manual = mergeEntries(st.Manual, base.Manual, local.Manual, func(m ManualInstall) string { return string(m.Kind) + ":" + normalizePathKey(m.Path) })
		st.GitProfiles = mergeEntries(st.GitProfiles, base.GitProfiles, local.GitProfiles, func(p GitProfile) string { return p.IdentityID() })
	})
}

// mergeEntries applies only locally changed entries, retaining other writers'
// additions and deletions. Conflicting changes to one entry are last-writer wins.
func mergeEntries[T any](latest, before, after []T, key func(T) string) []T {
	old, next := map[string]T{}, map[string]T{}
	for _, v := range before {
		old[key(v)] = v
	}
	for _, v := range after {
		next[key(v)] = v
	}
	out := make([]T, 0, len(latest)+len(after))
	seen := map[string]bool{}
	for _, v := range latest {
		k := key(v)
		seen[k] = true
		b, existed := old[k]
		a, remains := next[k]
		if existed && !remains {
			continue
		}
		if remains && (!existed || !reflect.DeepEqual(b, a)) {
			v = a
		}
		out = append(out, v)
	}
	for _, v := range after {
		k := key(v)
		if b, existed := old[k]; !seen[k] && (!existed || !reflect.DeepEqual(b, v)) {
			out = append(out, v)
		}
	}
	return out
}

type storeState struct {
	Config      Config          `json:"config"`
	Projects    []Project       `json:"projects"`
	Manual      []ManualInstall `json:"manual"`
	GitProfiles []GitProfile    `json:"gitProfiles,omitempty"`
}

func (s *Store) diskState() storeState {
	return storeState{s.Config, s.Projects, s.Manual, s.GitProfiles}
}

func (s *Store) installState(state storeState) {
	s.Config, s.Projects, s.Manual, s.GitProfiles = state.Config, state.Projects, state.Manual, state.GitProfiles
}

func readStoreState(path string) (storeState, error) {
	state := storeState{Config: DefaultConfig()}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(string(b)) == "null" {
		return state, fmt.Errorf("读取状态 %s: 状态必须是 JSON 对象", path)
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return state, fmt.Errorf("读取状态 %s: %w", path, err)
	}
	return state, nil
}

// ReadError reports the most recent reload error. Reading methods preserve the
// last committed snapshot on failure; execution paths should check this error.
func (s *Store) ReadError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readErr
}

func (s *Store) reloadLocked() error {
	state, err := readStoreState(s.path)
	s.readErr = err
	if err != nil {
		return err
	}
	s.installState(state)
	snapshot, _ := json.Marshal(state)
	var baseline storeState
	_ = json.Unmarshal(snapshot, &baseline)
	s.baseline = &baseline
	return nil
}

// atomicWriteFile uses a unique temporary file in the destination directory.
// The old file remains intact on every failure before the rename.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// withFileLock uses OS locks: process exit (including a crash) releases them.
// The lock file must remain in place so waiters always lock the same inode.
func withFileLock(path string, fn func() error) error {
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Close()
	return fn()
}

func (s *Store) restoreBaseline() {
	if s.baseline == nil {
		return
	}
	// Deep-copy the committed snapshot; callers may mutate maps afterwards.
	b, _ := json.Marshal(s.baseline)
	var state storeState
	_ = json.Unmarshal(b, &state)
	s.installState(state)
}

func (s *Store) transactionLocked(fn func(*Store)) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		s.restoreBaseline()
		return err
	}
	err := withFileLock(s.path, func() error {
		state, err := readStoreState(s.path)
		if err != nil {
			return err
		}
		candidate := &Store{path: s.path}
		candidate.installState(state)
		fn(candidate)
		b, err := json.MarshalIndent(candidate.diskState(), "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWriteFile(s.path, b, 0o644); err != nil {
			return err
		}
		s.installState(candidate.diskState())
		var baseline storeState
		_ = json.Unmarshal(b, &baseline)
		s.baseline = &baseline
		return nil
	})
	if err != nil {
		s.restoreBaseline()
	}
	return err
}

// Update reloads under a cross-process lock, applies fn to an isolated state,
// and publishes it in memory only after the atomic disk commit succeeds.
func (s *Store) Update(fn func(*Store)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transactionLocked(fn)
}

// ConfigCopy returns a copy of the config for reading.
func (s *Store) ConfigCopy() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reloadLocked()
	cfg := s.Config
	cfg.ScanRoots = append([]string(nil), cfg.ScanRoots...)
	if cfg.Defaults != nil {
		defaults := make(map[string]string, len(cfg.Defaults))
		for k, v := range cfg.Defaults {
			defaults[k] = v
		}
		cfg.Defaults = defaults
	}
	return cfg
}

// GitProfileList returns a copy of the remote-address bindings, sorted by key.
func (s *Store) GitProfileList() []GitProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reloadLocked()
	out := append([]GitProfile(nil), s.GitProfiles...)
	// Preserve insertion order within a key: it defines the legacy first identity.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ProjectList returns a copy of the projects, most recently opened first.
func (s *Store) ProjectList() []Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reloadLocked()
	out := append([]Project(nil), s.Projects...)
	for i := range out {
		out[i].Pins, _ = ReadProjectPins(out[i].Path)
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastOpened.After(out[j].LastOpened)
	})
	return out
}

// ProjectByPath finds a project by its directory.
func (s *Store) ProjectByPath(path string) (Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reloadLocked()
	for _, p := range s.Projects {
		if SamePath(p.Path, path) {
			p.Pins, _ = ReadProjectPins(p.Path)
			p.Tags = append([]string(nil), p.Tags...)
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
