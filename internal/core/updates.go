package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpdateLevel says how far an installed version trails the newest release of
// its own line.
type UpdateLevel int

const (
	// UpToDate means the line has no newer release.
	UpToDate UpdateLevel = iota
	// PatchUpdate means a newer patch exists in the same major.minor.
	PatchUpdate
	// MinorUpdate means the line moved to a new minor or a new major.minor
	// epoch, depending on how the SDK numbers its releases.
	MinorUpdate
	// MajorUpdate is a new major line: Node 20 → 22, Java 17 → 21.
	MajorUpdate
)

func (l UpdateLevel) String() string {
	switch l {
	case PatchUpdate:
		return "补丁"
	case MinorUpdate:
		return "小版本"
	case MajorUpdate:
		return "大版本"
	}
	return "最新"
}

// Token is the stable machine-readable form, used by --json.
func (l UpdateLevel) Token() string {
	switch l {
	case PatchUpdate:
		return "patch"
	case MinorUpdate:
		return "minor"
	case MajorUpdate:
		return "major"
	}
	return "none"
}

// EOLState is what the vendor publishes about the release line of a version.
type EOLState int

const (
	// EOLUnknown means the vendor publishes no end date for the line, or the
	// schedule could not be fetched.
	EOLUnknown EOLState = iota
	// EOLActive means the line still receives fixes.
	EOLActive
	// EOLSoon means the line ends within EOLWarnWindow.
	EOLSoon
	// EOLEnded means the line no longer receives fixes.
	EOLEnded
)

func (s EOLState) String() string {
	switch s {
	case EOLActive:
		return "维护中"
	case EOLSoon:
		return "即将停止维护"
	case EOLEnded:
		return "已停止维护"
	}
	return "未知"
}

// Token is the stable machine-readable form, used by --json.
func (s EOLState) Token() string {
	switch s {
	case EOLActive:
		return "active"
	case EOLSoon:
		return "soon"
	case EOLEnded:
		return "ended"
	}
	return "unknown"
}

// EOLWarnWindow is how close an end date has to be to count as a warning.
const EOLWarnWindow = 90 * 24 * time.Hour

// Cycle is one release line as the vendor's schedule describes it.
type Cycle struct {
	// Cycle is the line's version prefix: "20", "3.12", "1.27", "21".
	Cycle string `json:"cycle"`
	// EOL is the date the line stops receiving fixes, "" when the vendor
	// publishes no end date. Formats differ per product, so it stays a string.
	EOL string `json:"eol,omitempty"`
	// Latest is the newest release the schedule knows about.
	Latest string `json:"latest,omitempty"`
	// LTS marks a long-term-support line.
	LTS bool `json:"lts,omitempty"`
	// Support is when active support ends, for vendors that distinguish it
	// from security fixes.
	Support string `json:"support,omitempty"`
	// Open means the vendor publishes no end date for the line yet, which is
	// what a still-supported current release looks like.
	Open bool `json:"open,omitempty"`
}

// Update is one installed version measured against its release line.
type Update struct {
	Kind    Kind
	Version string
	Path    string
	Manager string
	// Latest is the newest stable release in the same line, which is what
	// Upgrade would install. Empty means the source has nothing for this
	// line any more.
	Latest string
	// Newest is the newest stable release of the SDK overall. It differs
	// from Latest only when a newer line exists, which is a decision rather
	// than an update.
	Newest string
	// Level compares Version with Latest.
	Level UpdateLevel
	// EOL is the published end date of the line, "" when unknown.
	EOL string
	// EOLState says whether the line is still maintained.
	EOLState EOLState
	// DaysLeft counts days until the line ends; negative once it has passed.
	// Zero when the date is unknown.
	DaysLeft int
}

// Outdated reports whether the line has a newer release.
func (u Update) Outdated() bool { return u.Level != UpToDate }

// HasNewerLine reports whether a release line newer than the installed one
// exists. Upgrading across it is never automatic.
func (u Update) HasNewerLine() bool {
	if u.Newest == "" || u.Latest == "" {
		return false
	}
	return ParseVersion(u.Newest).Compare(ParseVersion(u.Latest)) > 0
}

// LineOf is the release line a version belongs to: the major for Node, Java
// and Rust, major.minor for Python and Go, whose minor epoch is a
// compatibility boundary of its own (Python 3.12, Go 1.27).
func LineOf(kind Kind, v Version) string {
	switch kind {
	case Node, Java, Rust:
		return strconv.Itoa(v.Major())
	}
	if len(v.Parts) < 2 {
		return strconv.Itoa(v.Major())
	}
	return fmt.Sprintf("%d.%d", v.Major(), v.Minor())
}

// CycleFor finds the schedule entry covering a version: the longest cycle
// whose numbers are a prefix of the version's numbers. One rule covers every
// vendor, because each publishes the line exactly as it appears in versions.
func CycleFor(v Version, cycles []Cycle) (Cycle, bool) {
	var best Cycle
	bestLen := 0
	for _, c := range cycles {
		parts := ParseVersion(c.Cycle).Parts
		if len(parts) == 0 || len(parts) > len(v.Parts) {
			continue
		}
		match := true
		for i, p := range parts {
			if v.Parts[i] != p {
				match = false
				break
			}
		}
		if match && len(parts) > bestLen {
			best, bestLen = c, len(parts)
		}
	}
	return best, bestLen > 0
}

// daysUntil parses an ISO date and counts days from now, negative in the
// past.
func daysUntil(date string, now time.Time) (int, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return 0, false
	}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return int(t.Sub(today).Hours() / 24), true
}

// EOLStateFor reads a cycle's end date.
func EOLStateFor(c Cycle, now time.Time) (EOLState, int) {
	if c.Open {
		// The vendor answered "no end date", which is a supported line.
		return EOLActive, 0
	}
	days, ok := daysUntil(c.EOL, now)
	if !ok {
		return EOLUnknown, 0
	}
	switch {
	case days < 0:
		return EOLEnded, days
	case time.Duration(days)*24*time.Hour <= EOLWarnWindow:
		return EOLSoon, days
	}
	return EOLActive, days
}

// LevelFor compares an installed version with the newest of its line.
func LevelFor(installed, latest Version) UpdateLevel {
	if latest.Compare(installed) <= 0 {
		return UpToDate
	}
	switch {
	case installed.Major() != latest.Major():
		return MajorUpdate
	case installed.Minor() != latest.Minor():
		return MinorUpdate
	}
	return PatchUpdate
}

// remoteVersion is the number a release can be compared by. Rust publishes
// channel names ("stable") and puts the real version in the note; every other
// source puts it in Version.
func remoteVersion(r Remote) Version {
	if v := ParseVersion(r.Version); len(v.Parts) > 0 {
		return v
	}
	return ParseVersion(r.Note)
}

// buildUpdates pairs installed versions with the newest release of each line.
// Kinds without a remote list still get an EOL answer, so an imported Ruby
// can be flagged as unsupported even though EnvMgr cannot download it.
func buildUpdates(installed []Installed, remotes map[Kind][]Remote, cycles map[Kind][]Cycle, now time.Time) []Update {
	newest := map[Kind]Version{}
	lines := map[Kind]map[string]Version{}
	for kind, list := range remotes {
		byLine := map[string]Version{}
		for _, r := range list {
			v := remoteVersion(r)
			if len(v.Parts) == 0 || v.Pre != "" {
				continue // prereleases are not an upgrade target
			}
			line := LineOf(kind, v)
			if cur, ok := byLine[line]; !ok || v.Compare(cur) > 0 {
				byLine[line] = v
			}
			if cur, ok := newest[kind]; !ok || v.Compare(cur) > 0 {
				newest[kind] = v
			}
		}
		lines[kind] = byLine
	}

	out := make([]Update, 0, len(installed))
	for _, inst := range installed {
		v := ParseVersion(inst.Version)
		if len(v.Parts) == 0 {
			continue
		}
		u := Update{Kind: inst.Kind, Version: inst.Version, Path: inst.Path, Manager: inst.Manager}
		if latest, ok := lines[inst.Kind][LineOf(inst.Kind, v)]; ok {
			u.Latest = latest.String()
			u.Level = LevelFor(v, latest)
		}
		if n, ok := newest[inst.Kind]; ok {
			u.Newest = n.String()
		}
		if c, ok := CycleFor(v, cycles[inst.Kind]); ok {
			u.EOL = c.EOL
			u.EOLState, u.DaysLeft = EOLStateFor(c, now)
		}
		out = append(out, u)
	}

	// End-of-life first, then the biggest gap, then a stable order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].EOLState != out[j].EOLState {
			return out[i].EOLState > out[j].EOLState
		}
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level
		}
		if out[i].Kind != out[j].Kind {
			return kindOrder(out[i].Kind) < kindOrder(out[j].Kind)
		}
		return ParseVersion(out[i].Version).Compare(ParseVersion(out[j].Version)) > 0
	})
	return out
}

// ---- lifecycle schedule ----

// LifecycleTTL is how long a fetched release schedule stays fresh. Vendors
// publish end dates months ahead and change them rarely.
const LifecycleTTL = 24 * time.Hour

const lifecycleCacheVersion = 1

// lifecycleProducts maps a kind to its endoflife.date product slug.
var lifecycleProducts = map[Kind]string{
	Node:   "nodejs",
	Python: "python",
	Java:   "eclipse-temurin",
	Go:     "go",
	Rust:   "rust",
	Ruby:   "ruby",
}

// LifecycleCachePath is where release schedules are cached.
func LifecycleCachePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lifecycle.json"), nil
}

type lifecycleRecord struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Cycles    []Cycle   `json:"cycles"`
}

type lifecycleFile struct {
	Version int                        `json:"version"`
	Kinds   map[string]lifecycleRecord `json:"kinds"`
}

// lifecycleEntry is one row of the endoflife.date response. Dates arrive as
// either a string or false, and LTS as either a date or a bool, so the raw
// fields decode by hand.
type lifecycleEntry struct {
	Cycle   string          `json:"cycle"`
	EOL     json.RawMessage `json:"eol"`
	Support json.RawMessage `json:"support"`
	LTS     json.RawMessage `json:"lts"`
	Latest  string          `json:"latest"`
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func rawBool(raw json.RawMessage) bool {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	return rawText(raw) != "" // an LTS date also means "this line is LTS"
}

// rawFalse reports a field the vendor explicitly answered with false. That is
// different from a missing field: it means "no end date scheduled".
func rawFalse(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "false"
}

func parseLifecycle(body []byte) ([]Cycle, error) {
	var rows []lifecycleEntry
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("解析版本生命周期失败: %w", err)
	}
	out := make([]Cycle, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.Cycle) == "" {
			continue
		}
		out = append(out, Cycle{
			Cycle:   row.Cycle,
			EOL:     rawText(row.EOL),
			Support: rawText(row.Support),
			Latest:  row.Latest,
			LTS:     rawBool(row.LTS),
			Open:    rawFalse(row.EOL),
		})
	}
	return out, nil
}

func loadLifecycleCache() lifecycleFile {
	empty := lifecycleFile{Version: lifecycleCacheVersion, Kinds: map[string]lifecycleRecord{}}
	path, err := LifecycleCachePath()
	if err != nil {
		return empty
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var file lifecycleFile
	if err := json.Unmarshal(b, &file); err != nil || file.Version != lifecycleCacheVersion {
		return empty
	}
	if file.Kinds == nil {
		file.Kinds = map[string]lifecycleRecord{}
	}
	return file
}

func saveLifecycleCache(file lifecycleFile) {
	path, err := LifecycleCachePath()
	if err != nil {
		return
	}
	file.Version = lifecycleCacheVersion
	if file.Kinds == nil {
		return
	}
	b, err := json.Marshal(file)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = withFileLock(path, func() error { return atomicWriteFile(path, b, 0o644) })
}

// Lifecycles returns each vendor's release schedule, cached on disk. Partial
// failure is normal offline: the kinds that answered are returned, and the
// error says why the rest are missing.
func (s *Service) Lifecycles(ctx context.Context, force bool) (map[Kind][]Cycle, error) {
	ApplyNetworkConfig(s.Config())
	cache := loadLifecycleCache()
	out := map[Kind][]Cycle{}
	for name, rec := range cache.Kinds {
		if kind, ok := ParseKind(name); ok && len(rec.Cycles) > 0 {
			out[kind] = rec.Cycles
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	changed := false
	for _, kind := range Kinds {
		slug := lifecycleProducts[kind]
		if slug == "" {
			continue
		}
		rec, cached := cache.Kinds[string(kind)]
		if cached && !force && time.Since(rec.FetchedAt) < LifecycleTTL {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		wg.Add(1)
		go func(kind Kind, slug string) {
			defer wg.Done()
			body, err := FetchBytes(ctx, "https://endoflife.date/api/"+slug+".json")
			if err == nil {
				var cycles []Cycle
				cycles, err = parseLifecycle(body)
				if err == nil {
					mu.Lock()
					out[kind] = cycles
					cache.Kinds[string(kind)] = lifecycleRecord{FetchedAt: time.Now(), Cycles: cycles}
					changed = true
					mu.Unlock()
					return
				}
			}
			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("%s 的维护周期读取失败: %w", Info(kind).Name, err)
			}
			mu.Unlock()
		}(kind, slug)
	}
	wg.Wait()
	if changed {
		saveLifecycleCache(cache)
	}
	return out, firstErr
}

// ---- service API ----

// Updates compares every installed version with the newest release of its
// line. Only SDKs that are actually installed are queried, so a machine with
// Node and Python does not pay for four other catalogs.
func (s *Service) Updates(ctx context.Context, force bool) ([]Update, error) {
	installed := s.Installed()
	want := map[Kind]bool{}
	for _, inst := range installed {
		want[inst.Kind] = true
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	remotes := map[Kind][]Remote{}
	for _, kind := range Kinds {
		if !want[kind] || InstallerFor(kind) == nil {
			continue
		}
		wg.Add(1)
		go func(kind Kind) {
			defer wg.Done()
			list, err := s.ListRemote(ctx, kind, force)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s 的版本列表读取失败: %w", Info(kind).Name, err)
				}
				return
			}
			remotes[kind] = list
		}(kind)
	}
	wg.Wait()

	cycles, err := s.Lifecycles(ctx, force)
	if firstErr == nil {
		firstErr = err
	}
	return buildUpdates(installed, remotes, cycles, time.Now()), firstErr
}

// lineTarget picks the newest stable release of the version's own line from
// a source's list.
func lineTarget(list []Remote, kind Kind, version Version) (Remote, bool) {
	line := LineOf(kind, version)
	var best Remote
	found := false
	for _, r := range list {
		v := remoteVersion(r)
		if len(v.Parts) == 0 || v.Pre != "" {
			continue
		}
		if LineOf(kind, v) != line {
			continue
		}
		if !found || v.Compare(remoteVersion(best)) > 0 {
			best, found = r, true
		}
	}
	return best, found
}

// UpgradeTarget is the newest stable release in the installed version's own
// line: what Upgrade installs. Rust is excluded: its toolchains are updated
// by rustup, and installing the "stable" channel would not replace the
// version that is already there.
func (s *Service) UpgradeTarget(ctx context.Context, inst Installed) (Remote, error) {
	if inst.Kind == Rust {
		return Remote{}, fmt.Errorf("Rust 工具链由 rustup 管理，请运行 rustup update")
	}
	list, err := s.ListRemote(ctx, inst.Kind, false)
	if err != nil {
		return Remote{}, err
	}
	best, found := lineTarget(list, inst.Kind, ParseVersion(inst.Version))
	if !found {
		return Remote{}, fmt.Errorf("%s 的下载源里已经没有 %s 系列的版本", Info(inst.Kind).Name, LineOf(inst.Kind, ParseVersion(inst.Version)))
	}
	return best, nil
}

// Upgrade installs the newest release of the version's line. The old version
// stays on disk: other projects may still pin it, and removing it is one
// command away.
func (s *Service) Upgrade(ctx context.Context, inst Installed, progress Progress) (Installed, error) {
	if inst.Manager != "envmgr" {
		manager := inst.Manager
		if manager == "" {
			manager = "其他工具"
		}
		return Installed{}, fmt.Errorf("%s %s 由 %s 管理，请用该工具升级", Info(inst.Kind).Name, inst.Version, manager)
	}
	target, err := s.UpgradeTarget(ctx, inst)
	if err != nil {
		return Installed{}, err
	}
	if ParseVersion(inst.Version).Compare(remoteVersion(target)) >= 0 {
		return Installed{}, fmt.Errorf("%s %s 已经是该系列最新", Info(inst.Kind).Name, inst.Version)
	}
	return s.Install(ctx, target, progress)
}

// RepinExact rewrites .envmgr.json pins that name exactly the old version.
// A pin such as "20" already picks up the upgrade, and files owned by other
// tools (.nvmrc and friends) are reported rather than edited.
func (s *Service) RepinExact(old, new Installed) ([]Project, error) {
	var changed []Project
	oldVersion := ParseVersion(old.Version)
	for _, p := range s.Projects() {
		pins, err := ReadProjectPins(p.Path)
		if err != nil {
			continue
		}
		expr := pins[old.Kind]
		if expr == "" || ParseVersion(expr).Compare(oldVersion) != 0 {
			continue
		}
		if err := SetProjectPin(p.Path, old.Kind, new.Version); err != nil {
			return changed, fmt.Errorf("%s: %w", p.Name, err)
		}
		changed = append(changed, p)
	}
	return changed, nil
}
