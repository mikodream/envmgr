package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// A disk report answers the question an SDK manager usually cannot: how much
// space did this thing actually take, and what part of it is dead weight.
//
// Sizes are expensive — a Java JDK is tens of thousands of files — so every
// answer is cached per path and keyed by the directory's modification time.
// SDK installs never change after they land, so the cache stays valid until
// the directory itself is replaced.

// DiskEntry is one installed version and its footprint.
type DiskEntry struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`
	Manager string `json:"manager"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	// Known is false when the path could not be read; Size is then zero
	// rather than a lie.
	Known bool `json:"known"`
	// Note explains a size that is deliberately not counted, such as a
	// shared system directory.
	Note string `json:"note,omitempty"`
}

// Residue is something EnvMgr left behind that no version needs: a temp
// directory from an interrupted install, or an unlistable directory under the
// SDK root.
type Residue struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Label string `json:"label"`
	// ModTime is when it was last written, used to avoid touching a run that
	// is still in flight.
	ModTime time.Time `json:"modTime"`
}

// DiskReport is the whole picture: what is installed, what is cached, and
// what can be reclaimed.
type DiskReport struct {
	Entries []DiskEntry `json:"entries"`
	// Total is the size of every installed version; Managed is the part
	// EnvMgr itself put there.
	Total   int64 `json:"total"`
	Managed int64 `json:"managed"`
	// External is what other managers (nvm, pyenv, brew…) own. EnvMgr can
	// report it but never deletes it.
	External int64 `json:"external"`

	DataRoot string `json:"dataRoot"`
	CacheDir string `json:"cacheDir"`
	// CacheFiles counts downloaded archives; they are kept after unpacking so
	// a reinstall needs no network, and pruned on request.
	CacheFiles  int       `json:"cacheFiles"`
	CacheSize   int64     `json:"cacheSize"`
	CacheOldest time.Time `json:"cacheOldest,omitempty"`

	Residue     []Residue `json:"residue,omitempty"`
	ResidueSize int64     `json:"residueSize"`
}

// Reclaimable is the space a clean-up can give back.
func (r DiskReport) Reclaimable() int64 { return r.CacheSize + r.ResidueSize }

// CleanOptions says what to delete. Zero values mean "everything eligible".
type CleanOptions struct {
	// Cache removes downloaded archives.
	Cache bool
	// Residue removes orphan directories under the SDK root.
	Residue bool
	// MinAge skips anything touched more recently, so a download or an
	// install running in another process is never pulled out from under it.
	// Zero means diskGrace.
	MinAge time.Duration
}

// CleanResult reports what a clean-up actually freed.
type CleanResult struct {
	CacheFiles   int       `json:"cacheFiles"`
	CacheBytes   int64     `json:"cacheBytes"`
	Residue      []Residue `json:"residue,omitempty"`
	ResidueBytes int64     `json:"residueBytes"`
	// Skipped lists entries left alone because they were too new or locked.
	Skipped []string `json:"skipped,omitempty"`
}

// Freed is the total bytes reclaimed.
func (r CleanResult) Freed() int64 { return r.CacheBytes + r.ResidueBytes }

// diskGrace is how recent a file must be to be considered part of a running
// install. Ten minutes is longer than any unpack EnvMgr performs, so anything
// older is safely abandoned.
const diskGrace = 10 * time.Minute

// ---------------------------------------------------------------------------
// Size cache
// ---------------------------------------------------------------------------

const sizeCacheVersion = 1

type sizeStamp struct {
	Size int64 `json:"size"`
	// Mod is the directory mtime in Unix nanoseconds.
	Mod int64 `json:"mod"`
}

type sizeCacheFile struct {
	Version int                  `json:"version"`
	Paths   map[string]sizeStamp `json:"paths"`
}

// SizeCachePath is where measured install sizes are remembered.
func SizeCachePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sizes.json"), nil
}

// loadSizeCache reads the cache, treating any damage as an empty cache: a lost
// size costs one re-scan, never a wrong number.
func loadSizeCache() map[string]sizeStamp {
	path, err := SizeCachePath()
	if err != nil {
		return map[string]sizeStamp{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]sizeStamp{}
	}
	var f sizeCacheFile
	if err := json.Unmarshal(b, &f); err != nil || f.Version != sizeCacheVersion || f.Paths == nil {
		return map[string]sizeStamp{}
	}
	return f.Paths
}

func saveSizeCache(paths map[string]sizeStamp) {
	path, err := SizeCachePath()
	if err != nil {
		return
	}
	b, err := json.Marshal(sizeCacheFile{Version: sizeCacheVersion, Paths: paths})
	if err != nil {
		return
	}
	// Best effort: a read-only home directory should not fail a report.
	_ = atomicWriteFile(path, b, 0o644)
}

// measureSizes fills in e.Size for every entry, in parallel, reusing cached
// numbers whose directory has not changed since the last run.
func measureSizes(entries []DiskEntry) {
	cache := loadSizeCache()
	next := make(map[string]sizeStamp, len(entries))
	var mu sync.Mutex

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range entries {
		e := &entries[i]
		// A shared system root (or another SDK's home that happens to hold a
		// ruby) would report the whole tree as one install. Say so instead.
		if reason := sharedRootReason(e.Path); reason != "" {
			e.Known, e.Note = false, reason
			continue
		}
		mtime := dirModTime(e.Path)
		key := normalizePathKey(e.Path)
		if key == "" {
			key = e.Path
		}
		if stamp, ok := cache[key]; ok && stamp.Mod == mtime && stamp.Mod != 0 {
			e.Size, e.Known = stamp.Size, true
			mu.Lock()
			next[key] = stamp
			mu.Unlock()
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e *DiskEntry, key string, mtime int64) {
			defer wg.Done()
			defer func() { <-sem }()
			size := DirSize(e.Path)
			known := mtime != 0 || dirExists(e.Path)
			mu.Lock()
			e.Size, e.Known = size, known
			if known {
				next[key] = sizeStamp{Size: size, Mod: mtime}
			}
			mu.Unlock()
		}(e, key, mtime)
	}
	wg.Wait()
	saveSizeCache(next)
}

// dirModTime is the directory's mtime in Unix nanoseconds, or zero when it
// cannot be read.
func dirModTime(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}

// sharedRootReason names directories that belong to the operating system or to
// every SDK at once. Attributing their size to the one SDK discovered inside
// them would be wrong by gigabytes.
func sharedRootReason(path string) string {
	clean := filepath.Clean(path)
	switch runtime.GOOS {
	case "windows":
		if filepath.Dir(clean) == clean { // C:\ and other volume roots
			return "磁盘根目录，未计入"
		}
		for _, shared := range []string{`C:\Program Files`, `C:\Program Files (x86)`, `C:\ProgramData`} {
			if strings.EqualFold(clean, shared) {
				return "系统共享目录，未计入"
			}
		}
	default:
		switch clean {
		case "/", "/usr", "/usr/local", "/opt", "/opt/homebrew", "/bin", "/sbin", "/etc", "/Library", "/System":
			return "系统共享目录，未计入"
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && SamePath(clean, home) {
		return "用户主目录，未计入"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------

// DiskReport measures every installed version plus the download cache and any
// residue under the SDK root.
func (s *Service) DiskReport() (DiskReport, error) {
	cfg := s.Config()
	rep := DiskReport{}
	if root, err := DataDirRoot(cfg); err == nil {
		rep.DataRoot = root
	}
	cache, err := CacheDir(cfg)
	if err != nil {
		return rep, err
	}
	rep.CacheDir = cache

	seenPath := map[string]bool{}
	// The same install can be discovered twice under different names: macOS
	// firmlinks report /Users/… and /System/Volumes/Data/Users/… for one
	// directory. Comparing inodes folds those into one entry.
	var seenFile []os.FileInfo
	for _, inst := range s.Installed() {
		key := normalizePathKey(inst.Path)
		if key == "" || seenPath[key] {
			continue
		}
		seenPath[key] = true
		if st, err := os.Stat(inst.Path); err == nil {
			dup := false
			for _, prev := range seenFile {
				if os.SameFile(prev, st) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			seenFile = append(seenFile, st)
		}
		rep.Entries = append(rep.Entries, DiskEntry{
			ID:      inst.ID(),
			Kind:    inst.Kind,
			Version: inst.Version,
			Manager: inst.Manager,
			Path:    inst.Path,
		})
	}
	measureSizes(rep.Entries)
	// A cargo home holds the registry as well as the toolchain, so its number
	// is the whole Rust footprint rather than one compiler. Say which.
	for i := range rep.Entries {
		e := &rep.Entries[i]
		if e.Kind == Rust && dirExists(filepath.Join(e.Path, "registry")) {
			e.Note = "含 cargo 依赖缓存"
		}
	}
	sort.SliceStable(rep.Entries, func(i, j int) bool {
		if rep.Entries[i].Kind != rep.Entries[j].Kind {
			return kindIndex(rep.Entries[i].Kind) < kindIndex(rep.Entries[j].Kind)
		}
		return ParseVersion(rep.Entries[i].Version).Compare(ParseVersion(rep.Entries[j].Version)) > 0
	})
	for _, e := range rep.Entries {
		if !e.Known {
			continue
		}
		rep.Total += e.Size
		if e.Manager == "envmgr" {
			rep.Managed += e.Size
		} else {
			rep.External += e.Size
		}
	}

	if entries, err := cacheEntries(cache); err == nil {
		for _, f := range entries {
			rep.CacheFiles++
			rep.CacheSize += f.size
			if rep.CacheOldest.IsZero() || f.mod.Before(rep.CacheOldest) {
				rep.CacheOldest = f.mod
			}
		}
	}

	rep.Residue = residueOf(rep.DataRoot, installedPaths(rep.Entries))
	for _, r := range rep.Residue {
		rep.ResidueSize += r.Size
	}
	return rep, nil
}

// kindIndex keeps the report in the same order as the rest of the UI.
func kindIndex(k Kind) int {
	for i, candidate := range Kinds {
		if candidate == k {
			return i
		}
	}
	return len(Kinds)
}

// sameAsAny reports whether path is one of the directories in list, judging by
// inode rather than by spelling.
func sameAsAny(list []os.FileInfo, path string) bool {
	if len(list) == 0 {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, fi := range list {
		if os.SameFile(fi, st) {
			return true
		}
	}
	return false
}

// cacheEntry is one downloaded archive.
type cacheEntry struct {
	name string
	size int64
	mod  time.Time
}

// cacheEntries lists the downloaded archives, ignoring the lock files that sit
// beside them.
func cacheEntries(dir string) ([]cacheEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []cacheEntry
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, cacheEntry{name: e.Name(), size: st.Size(), mod: st.ModTime()})
	}
	return out, nil
}

// installedPaths is the set of directories the report already accounts for.
func installedPaths(entries []DiskEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func installedPathsOf(list []Installed) []string {
	out := make([]string, 0, len(list))
	for _, inst := range list {
		out = append(out, inst.Path)
	}
	return out
}

// residueOf finds directories under the SDK root that no installed version
// claims. EnvMgr itself creates two kinds: the .install-* scratch directory of
// an interrupted unpack, and a directory left after a failed extract.
//
// Membership is decided by file identity, not by string comparison. Deleting
// an installed version because its path arrived in a different but equivalent
// spelling would be the worst possible bug here, so a claimed directory is
// recognised even when discovery reported it through a firmlink.
func residueOf(dataRoot string, installs []string) []Residue {
	if dataRoot == "" {
		return nil
	}
	claimed := map[string]bool{}
	var claimedFiles []os.FileInfo
	for _, path := range installs {
		if key := normalizePathKey(path); key != "" {
			claimed[key] = true
		}
		if st, err := os.Stat(path); err == nil {
			claimedFiles = append(claimedFiles, st)
		}
	}
	var out []Residue
	for _, kind := range Kinds {
		dir := filepath.Join(dataRoot, string(kind))
		children, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, child := range children {
			name := child.Name()
			if !child.IsDir() {
				continue
			}
			full := filepath.Join(dir, name)
			if claimed[normalizePathKey(full)] || sameAsAny(claimedFiles, full) {
				continue
			}
			label := "未登记的目录"
			if strings.HasPrefix(name, ".") {
				label = "中断安装的临时目录"
			}
			st, err := child.Info()
			if err != nil {
				continue
			}
			out = append(out, Residue{
				Path:    full,
				Size:    DirSize(full),
				Label:   label,
				ModTime: st.ModTime(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

// ---------------------------------------------------------------------------
// Clean-up
// ---------------------------------------------------------------------------

// CleanDisk removes cached archives and/or residue. Installed versions are
// never touched: those go through Remove, which knows which projects use them.
func (s *Service) CleanDisk(opts CleanOptions) (CleanResult, error) {
	cfg := s.Config()
	grace := opts.MinAge
	if grace <= 0 {
		grace = diskGrace
	}
	cutoff := time.Now().Add(-grace)
	res := CleanResult{}
	var errs []error

	if opts.Cache {
		cache, err := CacheDir(cfg)
		if err != nil {
			return res, err
		}
		entries, err := cacheEntries(cache)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		for _, f := range entries {
			full := filepath.Join(cache, f.name)
			if f.mod.After(cutoff) {
				res.Skipped = append(res.Skipped, full)
				continue
			}
			// A held flock means another process is installing right now.
			if !tryLock(full) {
				res.Skipped = append(res.Skipped, full)
				continue
			}
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			res.CacheFiles++
			res.CacheBytes += f.size
		}
		// The lock files themselves are leftovers once their archive is gone.
		for _, e := range lockFiles(cache) {
			if st, err := e.Info(); err == nil && st.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(cache, e.Name()))
			}
		}
	}

	if opts.Residue {
		root, err := DataDirRoot(cfg)
		if err != nil {
			return res, err
		}
		for _, r := range residueOf(root, installedPathsOf(s.Installed())) {
			if r.ModTime.After(cutoff) {
				res.Skipped = append(res.Skipped, r.Path)
				continue
			}
			// The directory carries the install lock of an in-flight unpack.
			if !tryLock(r.Path) {
				res.Skipped = append(res.Skipped, r.Path)
				continue
			}
			if err := os.RemoveAll(r.Path); err != nil {
				errs = append(errs, err)
				continue
			}
			res.Residue = append(res.Residue, r)
			res.ResidueBytes += r.Size
		}
	}
	return res, errors.Join(errs...)
}

// lockFiles lists the *.lock siblings in dir.
func lockFiles(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".lock") {
			out = append(out, e)
		}
	}
	return out
}

// tryLock reports whether the install lock for path is free, and releases it
// immediately when it is. A lock that cannot be taken belongs to a running
// install.
//
// Opening a missing lock file would create one, littering the SDK root with
// empty files, so a lock that was never created means "free" without touching
// the disk.
func tryLock(path string) bool {
	lockPath := path + ".lock"
	if _, err := os.Stat(lockPath); err != nil {
		return true
	}
	lock := flock.New(lockPath)
	ok, err := lock.TryLock()
	if ok {
		_ = lock.Unlock()
	}
	_ = lock.Close()
	return err == nil && ok
}

// DescribeClean renders what a clean-up did, for the command line.
func DescribeClean(res CleanResult) string {
	var b strings.Builder
	if res.CacheFiles > 0 {
		fmt.Fprintf(&b, "清除下载缓存 %d 个归档，释放 %s", res.CacheFiles, HumanSize(res.CacheBytes))
	}
	if len(res.Residue) > 0 {
		if b.Len() > 0 {
			b.WriteString("；")
		}
		fmt.Fprintf(&b, "清除残留目录 %d 个，释放 %s", len(res.Residue), HumanSize(res.ResidueBytes))
	}
	if b.Len() == 0 {
		b.WriteString("没有可清理的内容")
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintf(&b, "（%d 项刚被写入或正被占用，已跳过）", len(res.Skipped))
	}
	return b.String()
}
