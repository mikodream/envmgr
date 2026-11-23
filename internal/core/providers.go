package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// Remote is one version offered for download.
type Remote struct {
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`
	// Vendor is where the build comes from, e.g. "temurin" or "corretto".
	// Empty means the SDK has a single source.
	Vendor string `json:"vendor,omitempty"`
	// VendorName is the readable form, e.g. "Eclipse Temurin".
	VendorName string `json:"vendorName,omitempty"`
	// Label says what the release is: LTS, Current, Latest, 归档.
	Label  string `json:"label,omitempty"`
	LTS    bool   `json:"lts,omitempty"`
	Size   int64  `json:"size,omitempty"`
	URL    string `json:"-"`
	SHA256 string `json:"-"`
	// File is the archive's name, used for the download cache.
	File string `json:"-"`
	// Note carries what the feed says, e.g. the release date.
	Note string `json:"note,omitempty"`
}

// InstallName is the directory a version is unpacked into. The default vendor
// keeps the plain version, so two builds of the same version from different
// vendors can live side by side without either overwriting the other.
func (r Remote) InstallName() string {
	if r.Vendor == "" || r.Vendor == DefaultVendor {
		return r.Version
	}
	return r.Version + "-" + r.Vendor
}

// DefaultVendor is the build used when the user does not pick one.
const DefaultVendor = "temurin"

// Installer downloads versions of one SDK.
type Installer interface {
	Kind() Kind
	// List returns the versions this machine can install, newest first.
	List(ctx context.Context, cfg Config) ([]Remote, error)
	// Fetch downloads, verifies and unpacks r, and returns the install.
	Fetch(ctx context.Context, r Remote, cfg Config, progress Progress) (Installed, error)
}

// Installers is every provider, keyed by kind.
func Installers() map[Kind]Installer {
	return map[Kind]Installer{
		Node:   nodeInstaller{},
		Python: pythonInstaller{},
		Go:     goInstaller{},
		Java:   javaInstaller{},
		Rust:   rustInstaller{},
	}
}

// InstallerFor returns the provider of a kind, or nil.
func InstallerFor(kind Kind) Installer {
	return Installers()[kind]
}

// ---- shared install pipeline ----

// installPath is where an EnvMgr-managed version lives:
// <data>/<kind>/<version>.
func installPath(cfg Config, kind Kind, version string) (string, error) {
	root, err := DataDirRoot(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, string(kind), version), nil
}

// installArchive runs the whole pipeline: download to the cache, check the
// digest, unpack into a temporary directory beside the destination, tidy the
// layout, then move it into place in one rename. A half-unpacked SDK never
// appears under the version it claims.
func installArchive(ctx context.Context, cfg Config, kind Kind, r Remote, progress Progress, tidy func(string) error) (Installed, error) {
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	if !validInstallComponent(r.File) || !validInstallComponent(r.InstallName()) {
		return Installed{}, fmt.Errorf("安装文件名或版本目录无效")
	}
	cache, err := CacheDir(cfg)
	if err != nil {
		return Installed{}, err
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return Installed{}, err
	}
	archive := filepath.Join(cache, r.File)
	unlock, err := lockInstallPath(ctx, archive)
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	reportInstallStage(ctx, "下载")
	if st, err := os.Stat(archive); err != nil || st.Size() == 0 {
		if err := Download(ctx, r.URL, archive, progress); err != nil {
			return Installed{}, err
		}
	} else if progress != nil {
		progress(st.Size(), st.Size())
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	reportInstallStage(ctx, "校验")
	if err := VerifySHA256(archive, r.SHA256); err != nil {
		// A corrupt archive would fail again on the next try.
		_ = os.Remove(archive)
		return Installed{}, err
	}

	dest, err := installPath(cfg, kind, r.InstallName())
	if err != nil {
		return Installed{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Installed{}, err
	}
	release, err := lockInstallPath(ctx, dest)
	if err != nil {
		return Installed{}, err
	}
	defer release()
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".install-*")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(tmp) // tmp is the unique directory returned by MkdirTemp above.
	reportInstallStage(ctx, "解包")
	if err := ExtractContext(ctx, archive, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return Installed{}, err
	}
	if err := Flatten(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return Installed{}, err
	}
	if tidy != nil {
		if err := tidy(tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return Installed{}, err
		}
	}
	probe := ScanInstall(Installed{Kind: kind, Version: r.Version, Path: tmp, Manager: "envmgr"})
	if probe.ExePath() == "" {
		return Installed{}, fmt.Errorf("安装包缺少 %s 可执行文件", kind)
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	reportInstallStage(ctx, "安装")
	if err := publishInstallDirectory(tmp, dest); err != nil {
		return Installed{}, err
	}
	return ScanInstall(Installed{Kind: kind, Version: r.Version, Path: dest, Manager: "envmgr"}), nil
}

// publishInstallDirectory restores the previous installation if publication fails.
func publishInstallDirectory(tmp, dest string) error {
	// Preserve a working previous install until the replacement is published.
	backup := tmp + "-previous"
	hadPrevious := false
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, dest); restoreErr != nil {
				return fmt.Errorf("安装失败：%v；恢复失败：%v，旧版本保留于 %s", err, restoreErr, backup)
			}
		}
		return err
	}
	if hadPrevious {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func validInstallComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\\`) && filepath.Base(name) == name
}

// lockInstallPath also coordinates project readiness, manual installs and CLI processes.
func lockInstallPath(ctx context.Context, path string) (func(), error) {
	lock := flock.New(path + ".lock")
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !ok {
		_ = lock.Close()
		return nil, fmt.Errorf("等待安装锁失败")
	}
	return func() { _ = lock.Close() }, nil
}

// downloader is the OS and architecture names this machine's feeds use.
type platform struct {
	os   string // feed name: windows, linux, darwin, ...
	arch string // feed name: x64, arm64, ...
	goos string
	goar string
}

func thisPlatform() platform {
	return platform{os: runtime.GOOS, arch: NormalizeArch(runtime.GOARCH), goos: runtime.GOOS, goar: runtime.GOARCH}
}

// ---- Node.js ----

type nodeInstaller struct{}

func (nodeInstaller) Kind() Kind { return Node }

type nodeRelease struct {
	Version  string   `json:"version"`
	Date     string   `json:"date"`
	Files    []string `json:"files"`
	LTS      any      `json:"lts"`
	Security bool     `json:"security"`
}

func (nodeInstaller) List(ctx context.Context, cfg Config) ([]Remote, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.NodeMirror), "/")
	if base == "" {
		base = "https://nodejs.org/dist"
	}
	body, err := FetchBytes(ctx, base+"/index.json")
	if err != nil {
		return nil, err
	}
	var releases []nodeRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("解析 Node 版本索引: %w", err)
	}
	p := thisPlatform()
	var out []Remote
	for _, rel := range releases {
		if !hasNodeFile(rel.Files, p) {
			continue
		}
		version := strings.TrimPrefix(rel.Version, "v")
		if strings.Contains(version, "-") {
			continue // prereleases are not what a project pins
		}
		label := "Current"
		if name, ok := rel.LTS.(string); ok && name != "" {
			label = "LTS · " + name
		}
		out = append(out, Remote{
			Kind: Node, Version: version, Label: label,
			LTS:  label != "Current",
			File: nodeFileName(version, p),
			URL:  base + "/v" + version + "/" + nodeFileName(version, p),
			Note: rel.Date,
		})
	}
	return out, nil
}

// hasNodeFile reports whether the release ships a build for this machine.
func hasNodeFile(files []string, p platform) bool {
	want := nodeFeedToken(p) + "-"
	for _, f := range files {
		if strings.HasPrefix(f, want) {
			return true
		}
	}
	return false
}

// nodeFeedToken is how nodejs.org/dist names a platform inside "files":
// win-x64-zip, osx-arm64-tar, linux-x64.
func nodeFeedToken(p platform) string {
	switch p.os {
	case "windows":
		return "win-" + p.arch
	case "darwin":
		return "osx-" + p.arch
	default:
		return p.os + "-" + p.arch
	}
}

// nodeFileName is the archive name: node-v20.11.1-win-x64.zip,
// node-v20.11.1-darwin-arm64.tar.gz, node-v20.11.1-linux-x64.tar.gz.
func nodeFileName(version string, p platform) string {
	switch p.os {
	case "windows":
		return "node-v" + version + "-win-" + p.arch + ".zip"
	case "darwin":
		return "node-v" + version + "-darwin-" + p.arch + ".tar.gz"
	default:
		return "node-v" + version + "-linux-" + p.arch + ".tar.gz"
	}
}

func (n nodeInstaller) Fetch(ctx context.Context, r Remote, cfg Config, progress Progress) (Installed, error) {
	// Node publishes digests next to the archive; verifying costs one small
	// request and catches a truncated mirror.
	if r.SHA256 == "" {
		base := strings.TrimSuffix(r.URL, "/"+r.File)
		if body, err := FetchBytes(ctx, base+"/SHASUMS256.txt"); err == nil {
			for _, line := range strings.Split(string(body), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == r.File {
					r.SHA256 = fields[0]
					break
				}
			}
		}
	}
	return installArchive(ctx, cfg, Node, r, progress, nil)
}

// ---- Python (python-build-standalone) ----

type pythonInstaller struct{}

func (pythonInstaller) Kind() Kind { return Python }

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Published  string `json:"published_at"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// pythonRepo is where portable CPython builds come from. They are the only
// official-ish way to install Python without a compiler.
const pythonRepo = "astral-sh/python-build-standalone"

func (pythonInstaller) List(ctx context.Context, cfg Config) ([]Remote, error) {
	// The GitHub API is rate limited for unauthenticated callers, so read
	// the release page's asset list instead: it is the same data, served as
	// HTML, with no quota.
	tag, err := githubLatestTag(ctx, pythonRepo)
	if err != nil {
		return nil, err
	}
	body, err := FetchBytes(ctx, "https://github.com/"+pythonRepo+"/releases/expanded_assets/"+tag)
	if err != nil {
		return nil, err
	}
	triple := Triple()
	// cpython-3.12.4+20240604-x86_64-unknown-linux-gnu-install_only.tar.gz
	re := regexp.MustCompile(`(cpython-([0-9][0-9.]*)\+[0-9]+-` + regexp.QuoteMeta(triple) + `-install_only\.tar\.gz)`)
	seen := map[string]bool{}
	var out []Remote
	for _, m := range re.FindAllStringSubmatch(string(body), -1) {
		file, version := m[1], m[2]
		if seen[version] {
			continue
		}
		seen[version] = true
		out = append(out, Remote{
			Kind: Python, Version: version, Label: "standalone",
			File: file, Note: tag,
			URL: "https://github.com/" + pythonRepo + "/releases/download/" + tag + "/" + file,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return ParseVersion(out[i].Version).Compare(ParseVersion(out[j].Version)) > 0
	})
	return out, nil
}

// githubLatestTag resolves "releases/latest" to its tag without downloading
// the page.
func githubLatestTag(ctx context.Context, repo string) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("%s：没有找到最新版本", repo)
	}
	tag := loc[strings.LastIndex(loc, "/")+1:]
	if tag == "" {
		return "", fmt.Errorf("%s：无法解析最新版本 %q", repo, loc)
	}
	return tag, nil
}

func (p pythonInstaller) Fetch(ctx context.Context, r Remote, cfg Config, progress Progress) (Installed, error) {
	return installArchive(ctx, cfg, Python, r, progress, nil)
}

// ---- Go ----

type goInstaller struct{}

func (goInstaller) Kind() Kind { return Go }

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	Files   []struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Version  string `json:"version"`
		SHA256   string `json:"sha256"`
		Size     int64  `json:"size"`
		Kind     string `json:"kind"`
	} `json:"files"`
}

func (goInstaller) List(ctx context.Context, cfg Config) ([]Remote, error) {
	body, err := FetchBytes(ctx, "https://go.dev/dl/?mode=json&include=all")
	if err != nil {
		return nil, err
	}
	var releases []goRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("解析 Go 版本索引: %w", err)
	}
	p := thisPlatform()
	var out []Remote
	for _, rel := range releases {
		for _, f := range rel.Files {
			if f.Kind != "archive" || f.OS != p.goos || f.Arch != p.goar {
				continue
			}
			if !rel.Stable {
				continue // rc and beta builds stay out of the list
			}
			version := strings.TrimPrefix(rel.Version, "go")
			label := "Stable"
			out = append(out, Remote{
				Kind: Go, Version: version, Label: label,
				Size: f.Size, URL: "https://go.dev/dl/" + f.Filename,
				File: f.Filename, SHA256: f.SHA256,
			})
			break
		}
	}
	return out, nil
}

func (g goInstaller) Fetch(ctx context.Context, r Remote, cfg Config, progress Progress) (Installed, error) {
	return installArchive(ctx, cfg, Go, r, progress, nil)
}

// ---- Java (several vendors of the same platform) ----

type javaInstaller struct{}

func (javaInstaller) Kind() Kind { return Java }

// List merges the Java builds of every vendor, so the page can offer the full
// minor history and let the user choose who built the JDK.
func (javaInstaller) List(ctx context.Context, cfg Config) ([]Remote, error) {
	return javaVendorList(ctx, thisPlatform())
}

func (j javaInstaller) Fetch(ctx context.Context, r Remote, cfg Config, progress Progress) (Installed, error) {
	return installArchive(ctx, cfg, Java, r, progress, func(dir string) error {
		// On macOS the JDK is a bundle; the SDK root is Contents/Home.
		home := filepath.Join(dir, "Contents", "Home")
		if dirExists(home) {
			inner, err := os.ReadDir(home)
			if err != nil {
				return err
			}
			for _, e := range inner {
				if err := os.Rename(filepath.Join(home, e.Name()), filepath.Join(dir, e.Name())); err != nil {
					return err
				}
			}
			return os.RemoveAll(filepath.Join(dir, "Contents"))
		}
		return nil
	})
}

// ---- Rust (through rustup, into EnvMgr's own directories) ----

type rustInstaller struct{}

func (rustInstaller) Kind() Kind { return Rust }

// rustChannels are the toolchains rustup publishes.
var rustChannels = []string{"stable", "beta", "nightly"}

func (rustInstaller) List(ctx context.Context, cfg Config) ([]Remote, error) {
	var out []Remote
	for i, ch := range rustChannels {
		version, err := rustChannelVersion(ctx, ch)
		if err != nil && i == 0 {
			return nil, err
		}
		label := "channel"
		if ch == "stable" {
			label = "Stable"
		}
		out = append(out, Remote{
			Kind: Rust, Version: ch, Label: label,
			URL:  "https://static.rust-lang.org/rustup/dist/" + Triple() + "/" + rustupName(),
			File: rustupName(), Note: version,
		})
	}
	return out, nil
}

// rustChannelVersion reads the version a channel currently points at.
func rustChannelVersion(ctx context.Context, channel string) (string, error) {
	body, err := FetchBytes(ctx, "https://static.rust-lang.org/dist/channel-rust-"+channel+".toml")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version = ") {
			v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "version = ")), `"`)
			if i := strings.IndexByte(v, ' '); i > 0 {
				v = v[:i]
			}
			return v, nil
		}
	}
	return "", nil
}

// rustupName is the installer's file name on this OS.
func rustupName() string {
	if runtime.GOOS == "windows" {
		return "rustup-init.exe"
	}
	return "rustup-init"
}

// rustupHome is EnvMgr's private RUSTUP_HOME, so installing Rust here never
// touches the user's ~/.rustup.
func rustupHome(cfg Config) (string, error) {
	root, err := DataDirRoot(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".rustup"), nil
}

func (rustInstaller) Fetch(ctx context.Context, r Remote, cfg Config, progress Progress) (Installed, error) {
	cache, err := CacheDir(cfg)
	if err != nil {
		return Installed{}, err
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return Installed{}, err
	}
	initPath := filepath.Join(cache, rustupName())
	unlock, err := lockInstallPath(ctx, initPath)
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	reportInstallStage(ctx, "下载")
	if _, err := os.Stat(initPath); err != nil {
		if err := Download(ctx, r.URL, initPath, progress); err != nil {
			return Installed{}, err
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(initPath, 0o755); err != nil {
			return Installed{}, err
		}
	}
	home, err := rustupHome(cfg)
	if err != nil {
		return Installed{}, err
	}
	root, err := DataDirRoot(cfg)
	if err != nil {
		return Installed{}, err
	}
	cargoHome := filepath.Join(root, ".cargo")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return Installed{}, err
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	reportInstallStage(ctx, "安装")
	cmd := exec.CommandContext(ctx, initPath,
		"--no-modify-path", "-y", "--profile", "minimal",
		"--default-toolchain", r.Version)
	cmd.Env = append(os.Environ(),
		"RUSTUP_HOME="+home,
		"CARGO_HOME="+cargoHome,
		"RUSTUP_INIT_SKIP_PATH_CHECK=yes",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Installed{}, fmt.Errorf("rustup 安装失败: %v\n%s", err, lastLines(string(out), 8))
	}
	toolchainDir := filepath.Join(home, "toolchains", r.Version+"-"+Triple())
	if !dirExists(toolchainDir) {
		// rustup may have named it after the resolved version.
		entries, _ := os.ReadDir(filepath.Join(home, "toolchains"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), r.Version) {
				toolchainDir = filepath.Join(home, "toolchains", e.Name())
				break
			}
		}
	}
	if !dirExists(toolchainDir) {
		return Installed{}, fmt.Errorf("rustup 已完成，但未找到 %s 工具链", r.Version)
	}
	inst := ScanInstall(Installed{Kind: Rust, Version: r.Version, Path: toolchainDir, Manager: "rustup"})
	inst.Env["RUSTUP_HOME"] = home
	inst.Env["CARGO_HOME"] = cargoHome
	return inst, nil
}

// lastLines keeps the tail of a program's output for an error message.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---- catalog with a short memory ----

// Catalog caches the download lists so the UI can switch pages without
// hitting every feed again.
type Catalog struct {
	mu    sync.Mutex
	cache map[Kind]catalogEntry
	ttl   time.Duration
}

type catalogEntry struct {
	list []Remote
	at   time.Time
	err  error
}

// NewCatalog returns a catalog with a ten minute memory.
func NewCatalog() *Catalog {
	return &Catalog{cache: map[Kind]catalogEntry{}, ttl: 10 * time.Minute}
}

// List returns the versions of a kind, from the cache when fresh.
func (c *Catalog) List(ctx context.Context, kind Kind, cfg Config, force bool) ([]Remote, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	entry, ok := c.cache[kind]
	c.mu.Unlock()
	if ok && !force && time.Since(entry.at) < c.ttl {
		return entry.list, entry.err
	}
	inst := InstallerFor(kind)
	if inst == nil {
		return nil, fmt.Errorf("%s 暂不支持下载安装", Info(kind).Name)
	}
	list, err := inst.List(ctx, cfg)
	if err == nil {
		err = ctx.Err()
	}
	// Failed and cancelled requests must remain retryable immediately.
	if err == nil {
		c.mu.Lock()
		c.cache[kind] = catalogEntry{list: list, at: time.Now()}
		c.mu.Unlock()
	}
	return list, err
}

// Invalidate forgets one kind.
func (c *Catalog) Invalidate(kind Kind) {
	c.mu.Lock()
	delete(c.cache, kind)
	c.mu.Unlock()
}
