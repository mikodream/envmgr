package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service is the one object the window and the command line both talk to.
// It owns the store, the discovery cache and the download catalog, and it is
// safe to use from any goroutine.
type Service struct {
	store   *Store
	catalog *Catalog

	mu         sync.Mutex
	installed  []Installed
	scanned    time.Time
	managers   []ManagerState
	changed    []func()
	gitChanged []func()
	// gitLoad may be replaced by tests; nil uses the production loader.
	gitLoad func(string) GitInfo
	// gitCache holds the last read of each directory's repository, because the
	// window asks on every frame and running git that often would show up as
	// stutter while a repository is large.
	gitCache map[string]gitCacheEntry
	// gitLoading marks the directories a read is running for, so overlapping
	// repaints do not start a second one.
	gitLoading map[string]bool
}

type gitCacheEntry struct {
	info GitInfo
	at   time.Time
}

// gitCacheTTL is how long a repository read is reused. Long enough that the
// draw loop never runs git, short enough that a commit made in another window
// shows up on its own.
const gitCacheTTL = 4 * time.Second

// NewService opens the store and does a first discovery.
func NewService() (*Service, error) {
	store, err := OpenStore()
	if err != nil {
		return nil, err
	}
	s := newService(store)
	s.Refresh()
	return s, nil
}

// NewFastService opens the store for a latency-sensitive read such as the
// shell hook's `env` call. It reuses the last full discovery while that
// snapshot is fresh and scans only when it is missing or stale.
func NewFastService() (*Service, error) {
	store, err := OpenStore()
	if err != nil {
		return nil, err
	}
	s := newService(store)
	s.hydrateDiscovery()
	return s, nil
}

func newService(store *Store) *Service {
	return &Service{
		store: store, catalog: NewCatalog(),
		gitCache:   map[string]gitCacheEntry{},
		gitLoading: map[string]bool{},
		gitLoad:    loadGitForService,
	}
}

// Git reads a directory's repository state without ever blocking the caller on
// an old entry.
//
// The first read for a directory runs git synchronously, because the page needs
// a real answer for its first paint and a wrong one would show "not a
// repository" for a frame. After that a stale entry is returned as-is and the
// read is repeated in the background; when it lands, listeners repaint. That
// keeps `git status` — which can take a long time on a big repository — off the
// draw loop.
func (s *Service) Git(dir string) GitInfo {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return GitInfo{}
	}
	s.mu.Lock()
	entry, ok := s.gitCache[dir]
	loading := s.gitLoading[dir]
	s.mu.Unlock()
	if !ok {
		// First sight of this directory: answer properly, once.
		return s.reloadGit(dir)
	}
	if time.Since(entry.at) >= gitCacheTTL && !loading {
		s.refreshGitLater(dir)
	}
	return entry.info
}

// PeekGit returns what is already known about a directory without running git.
//
// The project gallery draws a Git address on every tile, and asking git about
// every project on every first frame would block the window. A peek shows the
// address as soon as it has been read once and, when it has not, starts the read
// in the background so the next frame has it.
func (s *Service) PeekGit(dir string) (GitInfo, bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return GitInfo{}, false
	}
	s.mu.Lock()
	entry, ok := s.gitCache[dir]
	loading := s.gitLoading[dir]
	s.mu.Unlock()
	if !ok {
		if !loading {
			s.refreshGitLater(dir)
		}
		return GitInfo{}, false
	}
	if time.Since(entry.at) >= gitCacheTTL && !loading {
		s.refreshGitLater(dir)
	}
	return entry.info, true
}

// loadGitForService reads a repository with EnvMgr's own locations supplied, so a
// value a remote rule supplied is attributed to the rule rather than to the plain
// global config.
func loadGitForService(dir string) GitInfo {
	return LoadGitIn(dir, gitEnv{rulesDir: GitConfigDir(), globalPath: GlobalGitConfig()})
}

func (s *Service) loadGit(dir string) GitInfo {
	if s.gitLoad != nil {
		return s.gitLoad(dir)
	}
	return loadGitForService(dir)
}

// reloadGit runs git now and stores the answer.
func (s *Service) reloadGit(dir string) GitInfo {
	s.setGitLoading(dir, true)
	info := s.loadGit(dir)
	s.mu.Lock()
	if s.gitCache == nil {
		s.gitCache = map[string]gitCacheEntry{}
	}
	s.gitCache[dir] = gitCacheEntry{info: info, at: time.Now()}
	delete(s.gitLoading, dir)
	s.mu.Unlock()
	return info
}

// refreshGitLater re-reads a directory in the background, unless a read is
// already running for it.
func (s *Service) refreshGitLater(dir string) {
	s.mu.Lock()
	if s.gitLoading[dir] {
		s.mu.Unlock()
		return
	}
	if s.gitLoading == nil {
		s.gitLoading = map[string]bool{}
	}
	s.gitLoading[dir] = true
	s.mu.Unlock()
	go func() {
		info := s.loadGit(dir)
		s.mu.Lock()
		if s.gitCache == nil {
			s.gitCache = map[string]gitCacheEntry{}
		}
		s.gitCache[dir] = gitCacheEntry{info: info, at: time.Now()}
		delete(s.gitLoading, dir)
		s.mu.Unlock()
		s.notifyGitChanged()
	}()
}

func (s *Service) setGitLoading(dir string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gitLoading == nil {
		s.gitLoading = map[string]bool{}
	}
	if v {
		s.gitLoading[dir] = true
	} else {
		delete(s.gitLoading, dir)
	}
}

// RefreshGit re-reads a directory in the background, for the 刷新 button.
func (s *Service) RefreshGit(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	// Keep the last result available while the replacement is being read.
	s.refreshGitLater(dir)
}

// InvalidateGit forgets a directory's cached read.
func (s *Service) InvalidateGit(dir string) {
	s.mu.Lock()
	delete(s.gitCache, dir)
	s.mu.Unlock()
}

// InitGit turns a project directory into a repository.
func (s *Service) InitGit(dir string) (GitInfo, error) {
	info, err := InitGitRepo(dir)
	if err != nil {
		return info, err
	}
	s.mu.Lock()
	if s.gitCache == nil {
		s.gitCache = map[string]gitCacheEntry{}
	}
	s.gitCache[dir] = gitCacheEntry{info: info, at: time.Now()}
	s.mu.Unlock()
	s.Notify()
	return info, nil
}

// SetGitIdentity writes the project-level name and email.
func (s *Service) SetGitIdentity(dir, name, email string) error {
	if err := WriteGitIdentity(dir, name, email); err != nil {
		return err
	}
	// Re-read through the same path the page uses, so the new values are what
	// the next frame draws.
	s.reloadGit(dir)
	s.Notify()
	return nil
}

// SetGitRemote adds a destination or updates its fetch URL. Explicit push URLs
// remain separate, following git remote set-url's behavior.
func (s *Service) SetGitRemote(dir, name, url string) error {
	name, url = strings.TrimSpace(name), strings.TrimSpace(url)
	if err := validateGitRemote(dir, name); err != nil {
		return err
	}
	if url == "" || strings.ContainsAny(url, "\x00\r\n") {
		return errors.New("远端地址不能为空或包含换行")
	}
	// --get-url expands URL rewrite rules without connecting to the remote.
	if _, err := runGit(dir, "ls-remote", "--get-url", "--", url); err != nil {
		return fmt.Errorf("远端地址无效: %w", err)
	}
	command := "add"
	if remotes, err := runGit(dir, "remote"); err != nil {
		return fmt.Errorf("读取远端失败: %w", err)
	} else {
		for _, existing := range strings.Split(remotes, "\n") {
			if strings.TrimSpace(existing) == name {
				command = "set-url"
				break
			}
		}
	}
	if _, err := runGit(dir, "remote", command, "--", name, url); err != nil {
		return fmt.Errorf("保存远端失败: %w", err)
	}
	s.reloadGit(dir)
	s.Notify()
	return nil
}

func validateGitRemote(dir, name string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return errors.New("远端名称无效")
	}
	if _, err := runGit(dir, "check-ref-format", "refs/remotes/"+name); err != nil {
		return fmt.Errorf("远端名称无效: %w", err)
	}
	return nil
}

// RemoveGitRemote removes a named destination using Git's own config handling.
func (s *Service) RemoveGitRemote(dir, name string) error {
	name = strings.TrimSpace(name)
	if err := validateGitRemote(dir, name); err != nil {
		return err
	}
	if _, err := runGit(dir, "remote", "remove", "--", name); err != nil {
		return fmt.Errorf("删除远端失败: %w", err)
	}
	if err := clearGitRemoteSelection(dir, name); err != nil {
		return err
	}
	s.reloadGit(dir)
	s.Notify()
	return nil
}

// Store exposes the store for the settings page.
func (s *Service) Store() *Store { return s.store }

// GitProfiles returns the remote-address bindings, sorted by key.
func (s *Service) GitProfiles() []GitProfile { return s.store.GitProfileList() }

// SaveGitProfile updates an exact ID, or upserts a name/email pair at one
// normalized address. Global rules remain opt-in.
func (s *Service) SaveGitProfile(p GitProfile) error {
	p.Key = strings.TrimSpace(p.Key)
	p.Name = strings.TrimSpace(p.Name)
	p.Email = strings.TrimSpace(p.Email)
	if p.Key == "" {
		return errors.New("远端地址不能为空")
	}
	if p.Name == "" || p.Email == "" {
		return errors.New("用户名和邮箱都要填")
	}
	key, ok := NormalizeRemote(p.Key)
	if !ok {
		return errors.New("远端地址看起来不是有效的地址")
	}
	p.Key = key
	err := s.store.Update(func(st *Store) {
		normalizeGitProfileActives(st.GitProfiles)
		for i, existing := range st.GitProfiles {
			match := p.ID != "" && existing.IdentityID() == p.ID
			if p.ID == "" {
				match = strings.EqualFold(existing.Key, p.Key) && existing.Name == p.Name && existing.Email == p.Email
			}
			if match {
				p.ID = existing.IdentityID()
				p.Added = existing.Added
				p.Active = existing.Active
				// Moving an identity to an existing key preserves that key's selection.
				if !strings.EqualFold(existing.Key, p.Key) {
					p.Active = false
				}
				st.GitProfiles[i] = p
				normalizeGitProfileActives(st.GitProfiles)
				return
			}
		}
		if p.Added.IsZero() {
			p.Added = time.Now()
		}
		if p.ID == "" {
			p.ID = "git-" + rand.Text()
		}
		p.Active = false
		st.GitProfiles = append(st.GitProfiles, p)
		normalizeGitProfileActives(st.GitProfiles)
	})
	if err != nil {
		return err
	}
	if err := s.refreshInstalledGitRules(); err != nil {
		return err
	}
	s.invalidateAllGit()
	s.Notify()
	return nil
}

// DeleteGitProfile removes an exact identity ID. A legacy address selector
// removes every identity at that key.
func (s *Service) DeleteGitProfile(selector string) error {
	removedKeys := map[string]bool{}
	err := s.store.Update(func(st *Store) {
		byID := false
		for _, p := range st.GitProfiles {
			if p.IdentityID() == selector {
				byID = true
				break
			}
		}
		kept := st.GitProfiles[:0]
		for _, p := range st.GitProfiles {
			if byID && p.IdentityID() == selector || !byID && strings.EqualFold(p.Key, selector) {
				removedKeys[p.Key] = true
				continue
			}
			kept = append(kept, p)
		}
		st.GitProfiles = kept
		normalizeGitProfileActives(st.GitProfiles)
	})
	if err != nil {
		return err
	}
	if len(removedKeys) == 0 {
		return nil
	}
	if err := s.refreshInstalledGitRules(); err != nil {
		return err
	}
	for _, p := range s.GitProfiles() {
		for key := range removedKeys {
			if strings.EqualFold(key, p.Key) {
				delete(removedKeys, key)
			}
		}
	}
	for key := range removedKeys {
		_ = os.Remove(ProfileFile(key))
	}
	s.invalidateAllGit()
	s.Notify()
	return nil
}

// SelectGitProfile explicitly switches this repository's commit identity and
// the active global rule identity for the same address. It installs no rules.
func (s *Service) SelectGitProfile(dir, id string) error {
	var selected *GitProfile
	for _, p := range s.GitProfiles() {
		if p.IdentityID() == id {
			copy := p
			selected = &copy
			break
		}
	}
	if selected == nil {
		return errors.New("未找到 Git 身份")
	}
	if !selected.Valid() {
		return errors.New("这条绑定缺少用户名或邮箱")
	}
	if err := WriteGitIdentity(dir, selected.Name, selected.Email); err != nil {
		return err
	}
	if err := s.store.Update(func(st *Store) {
		for i := range st.GitProfiles {
			if strings.EqualFold(st.GitProfiles[i].Key, selected.Key) {
				st.GitProfiles[i].Active = st.GitProfiles[i].IdentityID() == id
			}
		}
	}); err != nil {
		return err
	}
	if err := s.refreshInstalledGitRules(); err != nil {
		return err
	}
	s.invalidateAllGit()
	s.reloadGit(dir)
	s.Notify()
	return nil
}

func (s *Service) refreshInstalledGitRules() error {
	if GitRulesInstalled() {
		return s.installGitRules()
	}
	return nil
}

// ApplyGitProfileTo writes a profile into one repository's own config. This is
// the way to bind an identity without installing global rules.
func (s *Service) ApplyGitProfileTo(dir string, p GitProfile) error {
	if !p.Valid() {
		return errors.New("这条绑定缺少用户名或邮箱")
	}
	return s.SetGitIdentity(dir, p.Name, p.Email)
}

// InstallGitRules (re)writes the global include rules for every profile.
func (s *Service) InstallGitRules() error {
	if err := s.installGitRules(); err != nil {
		return err
	}
	s.invalidateAllGit()
	s.Notify()
	return nil
}

// installGitRules is the shared implementation, without the repaint.
func (s *Service) installGitRules() error {
	return InstallGitRules(s.GitProfiles())
}

// UninstallGitRules removes the EnvMgr block from the global git config.
func (s *Service) UninstallGitRules() error {
	removed, err := UninstallGitRules()
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	s.invalidateAllGit()
	s.Notify()
	return nil
}

// GitRulesInstalled reports whether git is currently honouring the bindings.
func (s *Service) GitRulesInstalled() bool { return GitRulesInstalled() }

// MatchGitProfileFor finds the binding that applies to a remote URL.
func (s *Service) MatchGitProfileFor(remote string) *GitProfile {
	return MatchGitProfile(remote, s.GitProfiles())
}

// invalidateAllGit drops every cached repository read, so every page re-reads
// after a rule change. A rule changes what an unrelated repository resolves to,
// which is the whole point of it.
func (s *Service) invalidateAllGit() {
	s.mu.Lock()
	s.gitCache = map[string]gitCacheEntry{}
	s.mu.Unlock()
}

// Config is the current settings.
func (s *Service) Config() Config { return s.store.ConfigCopy() }

// SetConfig saves new settings and rediscovers, since the data directory may
// have moved.
func (s *Service) SetConfig(cfg Config) error {
	if err := s.store.Update(func(st *Store) { st.Config = cfg }); err != nil {
		return err
	}
	s.Refresh()
	return nil
}

// OnChange registers a listener that runs whenever discovery or the project
// list changes. The window uses it to repaint.
func (s *Service) OnChange(fn func()) {
	s.mu.Lock()
	s.changed = append(s.changed, fn)
	s.mu.Unlock()
}

// OnGitChange registers a repaint listener for background repository reads.
// These reads do not change SDK discovery or project resolution.
func (s *Service) OnGitChange(fn func()) {
	s.mu.Lock()
	s.gitChanged = append(s.gitChanged, fn)
	s.mu.Unlock()
}

func (s *Service) notifyGitChanged() {
	s.mu.Lock()
	listeners := append([]func(){}, s.gitChanged...)
	s.mu.Unlock()
	for _, fn := range listeners {
		fn()
	}
}

// Notify tells every listener that something changed.
func (s *Service) Notify() {
	s.mu.Lock()
	listeners := append([]func(){}, s.changed...)
	s.mu.Unlock()
	for _, fn := range listeners {
		fn()
	}
}

// Refresh rediscovers every SDK on this machine and tells the listeners.
func (s *Service) Refresh() []Installed {
	at := time.Now()
	list := DiscoverAll(s.Config())
	// Versions the user added by hand belong in the same list, so a project
	// can pin them like any other.
	list = append(list, s.ManualInstalls()...)
	s.mu.Lock()
	s.installed = list
	s.scanned = time.Now()
	s.mu.Unlock()
	saveDiscoverySnapshot(list, at)
	s.Notify()
	return list
}

// CachedInstalled returns a copy without discovery, even when the cache is old.
// UI notification consumers must not turn a state read into another scan.
func (s *Service) CachedInstalled() []Installed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Installed(nil), s.installed...)
}

// Installed returns the last discovery result, discovering first when it is
// older than a minute.
func (s *Service) Installed() []Installed {
	s.mu.Lock()
	list, at := s.installed, s.scanned
	s.mu.Unlock()
	if at.IsZero() || time.Since(at) > time.Minute {
		return s.Refresh()
	}
	return list
}

// ScannedAt is when discovery last ran.
func (s *Service) ScannedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanned
}

// Projects is the project list, most recently opened first.
func (s *Service) Projects() []Project { return s.store.ProjectList() }

// AddProject adds a directory to the managed list, or returns the existing
// entry.
func (s *Service) AddProject(path string) (Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Project{}, fmt.Errorf("目录不可用：%w", err)
	}
	if !st.IsDir() {
		return Project{}, errors.New("不是目录")
	}
	if existing, ok := s.store.ProjectByPath(abs); ok {
		return existing, nil
	}
	p := NewProject(abs)
	if err := s.store.Update(func(st *Store) {
		st.Projects = append(st.Projects, p)
	}); err != nil {
		return Project{}, err
	}
	s.Notify()
	return p, nil
}

// RemoveProject forgets a project. Files on disk are untouched.
func (s *Service) RemoveProject(id string) error {
	err := s.store.Update(func(st *Store) {
		out := st.Projects[:0]
		for _, p := range st.Projects {
			if p.ID != id {
				out = append(out, p)
			}
		}
		st.Projects = out
	})
	if err == nil {
		s.Notify()
	}
	return err
}

// Project looks one up by id.
func (s *Service) Project(id string) (Project, bool) {
	for _, p := range s.store.ProjectList() {
		if p.ID == id {
			return p, true
		}
	}
	return Project{}, false
}

// SetPin pins a version for a project. An empty version removes the pin, so
// the project follows its marker files again.
func (s *Service) SetPin(id string, kind Kind, version string) error {
	p, ok := s.Project(id)
	if !ok {
		return errors.New("未找到项目")
	}
	if err := SetProjectPin(p.Path, kind, version); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", ConfigFileName, err)
	}
	// Pins live exclusively in the project file. A metadata save must never
	// make a successful pin edit appear to have failed.
	s.Touch(id)
	return nil
}

// Touch marks a project as just used.
func (s *Service) Touch(id string) {
	_ = s.store.Update(func(st *Store) {
		for i := range st.Projects {
			if st.Projects[i].ID == id {
				st.Projects[i].LastOpened = time.Now()
			}
		}
	})
	s.Notify()
}

// Resolve works out what a project runs with.
func (s *Service) Resolve(p Project) []Resolution {
	cfg := s.Config()
	if err := s.store.ReadError(); err != nil {
		return []Resolution{{Source: "state.json", Err: err}}
	}
	return ResolveWithDefaults(p, s.Installed(), cfg.FollowMarkers, cfg.Defaults)
}

// ManualInstalls returns the versions the user added by hand, resolved into
// full installs against the disk as it is now.
func (s *Service) ManualInstalls() []Installed {
	entries := s.store.ManualCopy()
	out := make([]Installed, 0, len(entries))
	for _, m := range entries {
		inst := ScanInstall(Installed{
			Kind: m.Kind, Version: m.Version, Path: m.Path, Manager: ManualManager,
		})
		if inst.Available() {
			out = append(out, inst)
		}
	}
	return out
}

// AddManualInstall records an SDK the user pointed at by hand. When version is
// empty it is probed from the directory, which usually knows its own version.
func (s *Service) AddManualInstall(kind Kind, version, path, note string) (Installed, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return Installed{}, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Installed{}, fmt.Errorf("目录不存在: %s", abs)
	}
	if version == "" {
		version = ProbeVersion(kind, abs)
	}
	if version == "" {
		return Installed{}, fmt.Errorf("没能从 %s 读出%s版本号，请手动填写", abs, Info(kind).Name)
	}
	inst := ScanInstall(Installed{Kind: kind, Version: version, Path: abs, Manager: ManualManager})
	if !inst.Available() {
		return Installed{}, fmt.Errorf("%s 里没找到 %s，确认选对了目录", abs, Info(kind).Exe)
	}
	err = s.store.Update(func(st *Store) {
		for _, m := range st.Manual {
			if SamePath(m.Path, abs) && m.Kind == kind {
				return // already there
			}
		}
		st.Manual = append(st.Manual, ManualInstall{Kind: kind, Version: version, Path: abs, Note: note})
	})
	if err != nil {
		return Installed{}, err
	}
	s.Refresh()
	return inst, nil
}

// RemoveManualInstall forgets a hand-added version. The directory is left
// alone: EnvMgr did not create it and must not delete it.
func (s *Service) RemoveManualInstall(kind Kind, path string) error {
	err := s.store.Update(func(st *Store) {
		kept := st.Manual[:0]
		for _, m := range st.Manual {
			if m.Kind == kind && SamePath(m.Path, path) {
				continue
			}
			kept = append(kept, m)
		}
		st.Manual = kept
	})
	if err != nil {
		return err
	}
	s.Refresh()
	return nil
}

// EnvFor is the overlay for a project.
func (s *Service) EnvFor(p Project) Env {
	return BuildEnv(s.Resolve(p))
}

// EnvForDir resolves a directory that need not be a managed project. It walks
// up to the closest directory that declares versions, so a shell sitting in
// src/ gets the same environment as its project root. Outside every project the
// global defaults still apply: that is what makes a terminal useful in a
// scratch directory.
func (s *Service) EnvForDir(dir string) Env {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	if p, ok := s.store.ProjectByPath(abs); ok {
		return s.EnvFor(p)
	}
	root, ok := NearestProjectDir(abs)
	if !ok {
		cfg := s.Config()
		return BuildEnv(DefaultResolutions(s.Installed(), cfg.Defaults))
	}
	if p, ok := s.store.ProjectByPath(root); ok {
		return s.EnvFor(p)
	}
	return s.EnvFor(NewProject(root))
}

// Defaults is the global fallback of each SDK, keyed by kind.
func (s *Service) Defaults() map[Kind]string {
	out := map[Kind]string{}
	for name, expr := range s.Config().Defaults {
		if kind, ok := ParseKind(name); ok && strings.TrimSpace(expr) != "" {
			out[kind] = expr
		}
	}
	return out
}

// SetDefault makes a version the fallback for every directory that says
// nothing about that SDK. The expression is resolved against what is installed
// now, so a default never points at a version that is not there.
func (s *Service) SetDefault(kind Kind, expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return s.ClearDefault(kind)
	}
	inst, found := Best(s.Installed(), kind, expr)
	if !found {
		return fmt.Errorf("本机没有匹配 %s %s 的版本", Info(kind).Name, expr)
	}
	return s.store.Update(func(st *Store) {
		if st.Config.Defaults == nil {
			st.Config.Defaults = map[string]string{}
		}
		st.Config.Defaults[string(kind)] = inst.Version
	})
}

// ClearDefault removes the fallback of one SDK.
func (s *Service) ClearDefault(kind Kind) error {
	return s.store.Update(func(st *Store) {
		delete(st.Config.Defaults, string(kind))
	})
}

// ListRemote returns the downloadable versions of a kind.
func (s *Service) ListRemote(ctx context.Context, kind Kind, force bool) ([]Remote, error) {
	return s.catalog.List(ctx, kind, s.Config(), force)
}

// Install downloads and unpacks one remote version.
func (s *Service) Install(ctx context.Context, r Remote, progress Progress) (Installed, error) {
	inst := InstallerFor(r.Kind)
	if inst == nil {
		return Installed{}, fmt.Errorf("%s 暂不支持下载安装", Info(r.Kind).Name)
	}
	installed, err := inst.Fetch(ctx, r, s.Config(), progress)
	if err != nil {
		return Installed{}, err
	}
	s.Refresh()
	return installed, nil
}

// VersionInUseError refuses to delete a version that a project still pins.
type VersionInUseError struct {
	Kind     Kind
	Version  string
	Projects []Project
}

func (e *VersionInUseError) Error() string {
	names := make([]string, 0, len(e.Projects))
	for _, p := range e.Projects {
		names = append(names, p.Name)
	}
	return fmt.Sprintf("%s %s 仍被 %d 个项目使用：%s。先在这些项目里换版本，或加 --force 确认删除。",
		Info(e.Kind).Name, e.Version, len(e.Projects), strings.Join(names, "、"))
}

// VersionUsers lists the projects whose current environment resolves to
// kind@version. It is the reverse index behind the delete guard and answers
// "who still needs this version". Global defaults are not projects: deleting
// the fallback version is its own, separate check.
func (s *Service) VersionUsers(kind Kind, version string) []Project {
	var users []Project
	cfg := s.Config()
	installed := s.Installed()
	for _, p := range s.Projects() {
		for _, r := range Resolve(p, installed, cfg.FollowMarkers) {
			if r.Installed != nil && r.Kind == kind && r.Installed.Version == version {
				users = append(users, p)
				break
			}
		}
	}
	return users
}

// Remove deletes a version EnvMgr installed, refusing while a project still
// resolves to it.
func (s *Service) Remove(inst Installed) error { return s.remove(inst, false) }

// RemoveForced deletes a version even when projects still pin it. The caller
// has shown the user which projects those are.
func (s *Service) RemoveForced(inst Installed) error { return s.remove(inst, true) }

// remove does the deletion after the guard. Versions belonging to another
// manager are never touched: that would break the tool the user set up.
func (s *Service) remove(inst Installed, force bool) error {
	if !force {
		if users := s.VersionUsers(inst.Kind, inst.Version); len(users) > 0 {
			return &VersionInUseError{Kind: inst.Kind, Version: inst.Version, Projects: users}
		}
		// A global default is not a project, but deleting the version behind
		// it would quietly change every directory on the machine at once.
		if expr := strings.TrimSpace(s.Config().Defaults[string(inst.Kind)]); expr != "" {
			if match, found := Best([]Installed{inst}, inst.Kind, expr); found && match.Version == inst.Version {
				return fmt.Errorf("%s 是 %s 的全局默认版本。先运行 envmgr default --unset %s，或加 --force 确认删除。",
					inst.ID(), Info(inst.Kind).Name, inst.Kind)
			}
		}
	}
	if inst.Manager != "envmgr" && inst.Manager != "rustup" {
		return fmt.Errorf("%s 由 %s 管理，请在它那里卸载", inst.ID(), inst.Manager)
	}
	root, err := DataDirRoot(s.Config())
	if err != nil {
		return err
	}
	if inst.Manager == "rustup" {
		home, err := rustupHome(s.Config())
		if err != nil {
			return err
		}
		// Only toolchains inside EnvMgr's own rustup home are ours.
		if !strings.HasPrefix(normalizePathKey(inst.Path), normalizePathKey(home)) {
			return errors.New("该 Rust 工具链由系统的 rustup 管理")
		}
	} else if !strings.HasPrefix(normalizePathKey(inst.Path), normalizePathKey(root)) {
		return errors.New("该版本不在 EnvMgr 的数据目录中")
	}
	if err := os.RemoveAll(inst.Path); err != nil {
		return err
	}
	s.Refresh()
	return nil
}

// Managers returns the toolchain managers found on this machine, cached for
// half a minute.
func (s *Service) Managers() []ManagerState {
	s.mu.Lock()
	if s.managers != nil && time.Since(s.scanned) < 30*time.Second {
		list := s.managers
		s.mu.Unlock()
		return list
	}
	s.mu.Unlock()
	list := DetectManagers(s.Config())
	s.mu.Lock()
	s.managers = list
	s.mu.Unlock()
	return list
}

// InstallManager fetches a manager from its official release.
func (s *Service) InstallManager(ctx context.Context, id string, progress Progress) (ManagerState, error) {
	st, err := InstallManager(ctx, s.Config(), id, progress)
	if err != nil {
		return ManagerState{}, err
	}
	s.mu.Lock()
	s.managers = nil
	s.mu.Unlock()
	s.Refresh()
	return st, nil
}

// ScanProjects looks for project directories under root, up to a few levels
// deep, and returns the ones that carry a version marker or a lockfile.
func ScanProjects(root string, maxDepth int) []string {
	var found []string
	seen := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth || seen[normalizePathKey(dir)] {
			return
		}
		seen[normalizePathKey(dir)] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		if hasProjectMark(dir, entries) {
			found = append(found, dir)
			// A project can hold nested projects; keep looking but do not
			// descend into dependencies.
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" ||
				name == "target" || name == "dist" || name == "build" || name == ".git" ||
				name == "__pycache__" || name == ".venv" || name == "venv" {
				continue
			}
			walk(filepath.Join(dir, name), depth+1)
		}
	}
	walk(root, 0)
	sort.Strings(found)
	return found
}

// hasProjectMark reports whether a directory looks like a project.
func hasProjectMark(dir string, entries []os.DirEntry) bool {
	markers := map[string]bool{}
	for _, kind := range Kinds {
		for _, m := range Info(kind).Markers {
			markers[m] = true
		}
	}
	markers[ConfigFileName] = true
	markers[".tool-versions"] = true
	markers[".git"] = true
	for _, e := range entries {
		if markers[e.Name()] {
			return true
		}
	}
	return false
}
