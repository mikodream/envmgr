package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Java ships as several independent builds of the same specification. They
// differ in who patches them and which licence they carry, so the version list
// is a version × vendor matrix: the user picks the JDK build they trust, not
// just "java 21".
type javaVendor struct {
	ID   string
	Name string
	// Home is the project page, for the "where does this come from" question.
	Home string
	// All lists the builds of one major version this vendor offers.
	All func(ctx context.Context, p platform, major int) ([]Remote, error)
}

// javaVendors is the source list of the download page, in display order.
// Every entry was checked against the vendor's own public endpoint.
var javaVendors = []javaVendor{
	{"temurin", "Eclipse Temurin", "https://adoptium.net", temurinReleases},
	{"corretto", "Amazon Corretto", "https://aws.amazon.com/corretto", correttoRelease},
	{"zulu", "Azul Zulu Community", "https://www.azul.com/downloads", zuluReleases},
	{"liberica", "BellSoft Liberica JDK", "https://bell-sw.com/libericajdk", libericaReleases},
	{"microsoft", "Microsoft Build of OpenJDK", "https://www.microsoft.com/openjdk", microsoftRelease},
}

// JavaVendors lists the vendors the download page can offer.
func JavaVendors() []javaVendor { return javaVendors }

// JavaVendorNames returns the readable vendor names in display order.
func JavaVendorNames() []string {
	out := make([]string, 0, len(javaVendors))
	for _, v := range javaVendors {
		out = append(out, v.Name)
	}
	return out
}

// JavaVendorName turns a vendor id into its readable form. The table is
// spelled out rather than read from javaVendors: the vendor functions call
// this while javaVendors is still being initialised.
func JavaVendorName(id string) string {
	if name, ok := javaVendorNames[id]; ok {
		return name
	}
	if id == "" {
		return "官方源"
	}
	return id
}

var javaVendorNames = map[string]string{
	"temurin":   "Eclipse Temurin",
	"corretto":  "Amazon Corretto",
	"zulu":      "Azul Zulu Community",
	"liberica":  "BellSoft Liberica JDK",
	"microsoft": "Microsoft Build of OpenJDK",
}

// JavaVendorHome is where a vendor's builds come from.
func JavaVendorHome(id string) string {
	for _, v := range javaVendors {
		if v.ID == id {
			return v.Home
		}
	}
	return ""
}

// javaMajorList is the major versions worth asking every vendor about: what
// Adoptium publishes, plus the LTS lines older than what it still serves from
// the main API. Java 8 builds are still published as 1.8.0 by several vendors,
// and nobody wants them missing from the list.
var javaLTSMajors = []int{8, 11, 17, 21, 25}

func javaMajors(ctx context.Context) []int {
	seen := map[int]bool{}
	var out []int
	add := func(m int) {
		if m > 0 && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	var available struct {
		Releases []int `json:"available_releases"`
	}
	if body, err := FetchBytes(ctx, "https://api.adoptium.net/v3/info/available_releases"); err == nil {
		if json.Unmarshal(body, &available) == nil {
			for _, m := range available.Releases {
				add(m)
			}
		}
	}
	for _, m := range javaLTSMajors {
		add(m)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// ---- Eclipse Temurin, through Adoptium ----

type adoptiumRelease struct {
	ReleaseName string `json:"release_name"`
	VersionData struct {
		Semver string `json:"semver"`
		Major  int    `json:"major"`
	} `json:"version_data"`
	Binaries []struct {
		Architecture string `json:"architecture"`
		ImageType    string `json:"image_type"`
		OS           string `json:"os"`
		Package      struct {
			Name     string `json:"name"`
			Link     string `json:"link"`
			Size     int64  `json:"size"`
			Checksum string `json:"checksum"`
		} `json:"package"`
	} `json:"binaries"`
}

// temurinReleases asks Adoptium for every GA build of one major. Temurin keeps
// the whole back catalogue available, so this is the vendor that gives the
// complete minor history.
func temurinReleases(ctx context.Context, p platform, major int) ([]Remote, error) {
	osName := adoptiumOS(p.goos)
	arch := adoptiumArch(p.arch)
	if osName == "" || arch == "" {
		return nil, fmt.Errorf("Adoptium 不支持 %s/%s", p.goos, p.goar)
	}
	url := fmt.Sprintf("https://api.adoptium.net/v3/assets/feature_releases/%d/ga"+
		"?architecture=%s&image_type=jdk&os=%s&project=jdk&sort_order=DESC&vendor=eclipse",
		major, arch, osName)
	releases, err := adoptiumFeatureReleases(ctx, major, url)
	if err != nil {
		return nil, err
	}
	out := make([]Remote, 0, len(releases))
	for _, rel := range releases {
		for _, b := range rel.Binaries {
			if b.ImageType != "jdk" || b.OS != osName || b.Architecture != arch || b.Package.Link == "" {
				continue
			}
			version := cleanAdoptiumVersion(rel.VersionData.Semver, rel.ReleaseName)
			if version == "" {
				continue
			}
			out = append(out, Remote{
				Kind: Java, Version: normalizeJavaVersion(version),
				Vendor: "temurin", VendorName: JavaVendorName("temurin"),
				Label: javaLabel(major), LTS: isJavaLTS(major),
				Size: b.Package.Size, URL: b.Package.Link, File: b.Package.Name,
				SHA256: b.Package.Checksum,
			})
			break
		}
	}
	return out, nil
}

// adoptiumPageSize is deliberately small. Adoptium's paging walks correctly
// with a small page — page 1 is the newest builds, page 2 the next ones back —
// while a large page jumps into the middle of the history and can answer 404
// outright. Asking for 30 in one request is what silently emptied the Java 21,
// 25 and 27 lists and hid the newest Temurin builds of every major.
const adoptiumPageSize = 3

// adoptiumMaxPages caps the history walk at 18 builds per major, which is well
// past the point where a build is still interesting.
const adoptiumMaxPages = 6

func adoptiumFeatureReleases(ctx context.Context, major int, base string) ([]adoptiumRelease, error) {
	var all []adoptiumRelease
	var lastErr error
	for page := 1; page <= adoptiumMaxPages; page++ {
		url := fmt.Sprintf("%s&page=%d&page_size=%d", base, page, adoptiumPageSize)
		body, err := FetchBytes(ctx, url)
		if err != nil {
			// A page that fails after the first one just ends the walk: what
			// has been collected is still a valid, newest-first history.
			if page == 1 {
				lastErr = err
			}
			break
		}
		var releases []adoptiumRelease
		if err := json.Unmarshal(body, &releases); err != nil {
			if page == 1 {
				lastErr = fmt.Errorf("解析 Temurin %d 版本: %w", major, err)
			}
			break
		}
		all = append(all, releases...)
		if len(releases) < adoptiumPageSize {
			break
		}
	}
	if len(all) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("Adoptium 没有返回 Java %d", major)
		}
		return nil, lastErr
	}
	return all, nil
}

// javaLabel names the platform line, not the vendor: the vendor is its own
// field, and repeating it in the label shows up twice in the UI.
func javaLabel(major int) string { return fmt.Sprintf("Java %d", major) }

func cleanAdoptiumVersion(semver, releaseName string) string {
	v := semver
	if i := strings.IndexAny(v, "+"); i > 0 {
		v = v[:i]
	}
	if v == "" {
		v = strings.TrimPrefix(releaseName, "jdk-")
		if i := strings.IndexAny(v, "+"); i > 0 {
			v = v[:i]
		}
	}
	return strings.TrimSpace(v)
}

// ---- Amazon Corretto ----

// correttoRelease resolves the stable "latest" link of one major. Corretto
// publishes no version index any more, but the download itself redirects to a
// versioned file name, which is the version.
func correttoRelease(ctx context.Context, p platform, major int) ([]Remote, error) {
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos"}[p.goos]
	arch := map[string]string{"x64": "x64", "arm64": "aarch64"}[p.arch]
	if osName == "" || arch == "" {
		return nil, fmt.Errorf("Corretto 不支持 %s/%s", p.goos, p.goar)
	}
	url := fmt.Sprintf("https://corretto.aws/downloads/latest/amazon-corretto-%d-%s-%s-jdk.zip", major, arch, osName)
	final, size, err := resolveDownload(ctx, url)
	if err != nil {
		return nil, err
	}
	raw := correttoVersionFromURL(final)
	if raw == "" {
		return nil, fmt.Errorf("无法从 %s 解析 Corretto 版本", final)
	}
	version := normalizeJavaVersion(raw)
	return []Remote{{
		Kind: Java, Version: version, Vendor: "corretto", VendorName: JavaVendorName("corretto"),
		Label: javaLabel(major), LTS: isJavaLTS(major),
		Size: size, URL: final, File: archiveName(final),
	}}, nil
}

var correttoVerRe = regexp.MustCompile(`amazon-corretto-([0-9]+(?:\.[0-9]+)*)-`)

func correttoVersionFromURL(u string) string {
	m := correttoVerRe.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	return m[1]
}

// ---- Azul Zulu Community ----

type zuluPackage struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	JavaVersion []int  `json:"java_version"`
	DistroVer   []int  `json:"distro_version"`
	Product     string `json:"product"`
}

// zuluReleases reads Azul's metadata API. It returns one entry per build, with
// CRaC and JavaFX bundles mixed in; only the plain JDK is interesting here.
func zuluReleases(ctx context.Context, p platform, major int) ([]Remote, error) {
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos"}[p.goos]
	arch := map[string]string{"x64": "x64", "arm64": "aarch64"}[p.arch]
	if osName == "" || arch == "" {
		return nil, fmt.Errorf("Zulu 不支持 %s/%s", p.goos, p.goar)
	}
	url := fmt.Sprintf("https://api.azul.com/metadata/v1/zulu/packages/"+
		"?java_version=%d&os=%s&arch=%s&archive_type=zip&java_package_type=jdk"+
		"&release_status=ga&page_size=100&page=1", major, osName, arch)
	body, err := FetchBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	var pkgs []zuluPackage
	if err := json.Unmarshal(body, &pkgs); err != nil {
		return nil, fmt.Errorf("解析 Zulu 版本: %w", err)
	}
	byVersion := map[string]Remote{}
	for _, pkg := range pkgs {
		if pkg.DownloadURL == "" || !isPlainZuluJDK(pkg.Name) {
			continue
		}
		version := numericVersion(pkg.JavaVersion)
		if version == "" {
			continue
		}
		// One build per minor: the newest of that minor wins.
		if prev, ok := byVersion[version]; ok && !newerBuild(pkg.JavaVersion, prev) {
			continue
		}
		byVersion[version] = Remote{
			Kind: Java, Version: version, Vendor: "zulu", VendorName: JavaVendorName("zulu"),
			Label: javaLabel(major), LTS: isJavaLTS(major),
			URL: pkg.DownloadURL, File: archiveName(pkg.DownloadURL),
		}
	}
	out := make([]Remote, 0, len(byVersion))
	for _, r := range byVersion {
		out = append(out, r)
	}
	return out, nil
}

// isPlainZuluJDK rejects the CRaC and JavaFX flavours, and the source bundles.
func isPlainZuluJDK(name string) bool {
	n := strings.ToLower(name)
	if !strings.Contains(n, "jdk") {
		return false
	}
	for _, bad := range []string{"-crac", "-fx", "src", "jre"} {
		if strings.Contains(n, bad) {
			return false
		}
	}
	return true
}

// newerBuild reports whether a is a later build than the one already stored.
func newerBuild(javaVersion []int, prev Remote) bool {
	return compareNumeric(javaVersion, versionParts(prev.Version)) > 0
}

// ---- BellSoft Liberica ----

type libericaRelease struct {
	Version      string `json:"version"`
	DownloadURL  string `json:"downloadUrl"`
	PackageType  string `json:"packageType"`
	BundleType   string `json:"bundleType"`
	Architecture string `json:"architecture"`
	Bitness      int    `json:"bitness"`
	OS           string `json:"os"`
	Size         int64  `json:"size"`
	LTS          bool   `json:"LTS"`
	Latest       bool   `json:"latest"`
}

// libericaReleases reads BellSoft's catalogue. The API rejects unknown query
// parameters with a 400, so only the documented ones go in the URL.
func libericaReleases(ctx context.Context, p platform, major int) ([]Remote, error) {
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos"}[p.goos]
	// BellSoft calls 64-bit x86 "x86" and ARM "arm".
	arch := map[string]string{"x64": "x86", "arm64": "arm"}[p.arch]
	if osName == "" || arch == "" {
		return nil, fmt.Errorf("Liberica 不支持 %s/%s", p.goos, p.goar)
	}
	url := fmt.Sprintf("https://api.bell-sw.com/v1/liberica/releases"+
		"?output=json&version-feature=%d&os=%s&arch=%s&bundle-type=jdk&bitness=64", major, osName, arch)
	body, err := FetchBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	var rels []libericaRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, fmt.Errorf("解析 Liberica 版本: %w", err)
	}
	best := map[string]Remote{}
	for _, rel := range rels {
		if !libericaArchiveFor(rel, osName, arch) {
			continue
		}
		version := normalizeJavaVersion(rel.Version)
		if version == "" {
			continue
		}
		if _, ok := best[version]; ok {
			continue // the API returns newest first
		}
		best[version] = Remote{
			Kind: Java, Version: version, Vendor: "liberica", VendorName: JavaVendorName("liberica"),
			Label: javaLabel(major), LTS: isJavaLTS(major),
			Size: rel.Size, URL: rel.DownloadURL, File: archiveName(rel.DownloadURL),
		}
	}
	out := make([]Remote, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	return out, nil
}

// libericaArchiveFor keeps only the plain JDK archive of this platform: no
// installers, no full/lite bundles, no source tarballs.
func libericaArchiveFor(rel libericaRelease, osName, arch string) bool {
	if rel.DownloadURL == "" || rel.BundleType != "jdk" || rel.OS != osName {
		return false
	}
	if rel.Architecture != arch || rel.Bitness != 64 {
		return false
	}
	if osName == "windows" || osName == "macos" {
		return rel.PackageType == "zip"
	}
	return rel.PackageType == "tar.gz"
}

// ---- Microsoft Build of OpenJDK ----

// microsoftRelease resolves Microsoft's stable aka.ms link. Like Corretto, the
// redirect names the exact build.
func microsoftRelease(ctx context.Context, p platform, major int) ([]Remote, error) {
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos"}[p.goos]
	arch := map[string]string{"x64": "x64", "arm64": "aarch64"}[p.arch]
	if osName == "" || arch == "" {
		return nil, fmt.Errorf("Microsoft 不支持 %s/%s", p.goos, p.goar)
	}
	ext := "zip"
	if osName == "linux" || osName == "macos" {
		ext = "tar.gz"
	}
	url := fmt.Sprintf("https://aka.ms/download-jdk/microsoft-jdk-%d-%s-%s.%s", major, osName, arch, ext)
	final, size, err := resolveDownload(ctx, url)
	if err != nil {
		return nil, err
	}
	// A vendor that has no build for this major answers with a tiny error page
	// instead of redirecting to an archive, so the link that did not move is
	// the signal that this version simply does not exist.
	if final == url {
		return nil, fmt.Errorf("Microsoft 未发布 Java %d", major)
	}
	raw := microsoftVersionFromURL(final)
	if raw == "" || strings.Count(raw, ".") < 1 {
		return nil, fmt.Errorf("Microsoft 未发布 Java %d", major)
	}
	version := normalizeJavaVersion(raw)
	return []Remote{{
		Kind: Java, Version: version, Vendor: "microsoft", VendorName: JavaVendorName("microsoft"),
		Label: javaLabel(major), LTS: isJavaLTS(major),
		Size: size, URL: final, File: archiveName(final),
	}}, nil
}

var microsoftVerRe = regexp.MustCompile(`microsoft-jdk-([0-9]+(?:\.[0-9]+)*)-`)

func microsoftVersionFromURL(u string) string {
	m := microsoftVerRe.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	return m[1]
}

// ---- shared helpers ----

// javaVendorList gathers every vendor's builds, newest first. A vendor that
// fails is skipped: one unreachable source must not empty the page, so the
// error is only returned when nothing at all came back.
func javaVendorList(ctx context.Context, p platform) ([]Remote, error) {
	majors := javaMajors(ctx)
	if len(majors) == 0 {
		return nil, fmt.Errorf("无法读取 Java 版本索引")
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  []Remote
		errs []string
		ok   int
	)
	for _, vendor := range javaVendors {
		for _, major := range majors {
			wg.Add(1)
			go func(vendor javaVendor, major int) {
				defer wg.Done()
				list, err := vendor.All(ctx, p, major)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s %d: %v", vendor.Name, major, err))
					return
				}
				ok++
				out = append(out, list...)
			}(vendor, major)
		}
	}
	wg.Wait()
	if len(out) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("所有下载源都失败了：%s", strings.Join(uniqueStrings(errs), "；"))
		}
		return nil, fmt.Errorf("下载源没有返回可用的 JDK")
	}
	sortJavaRemotes(out)
	// One row per build: Temurin publishes several builds of the same version
	// (11.0.14+9 and +10), and the list should not show both.
	deduped := out[:0]
	seen := map[string]bool{}
	for _, r := range out {
		key := r.Vendor + "@" + r.Version
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, r)
	}
	return deduped, nil
}

// sortJavaRemotes puts the newest version first, and within one version the
// default vendor first, then alphabetically by vendor.
func sortJavaRemotes(list []Remote) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if c := ParseVersion(a.Version).Compare(ParseVersion(b.Version)); c != 0 {
			return c > 0
		}
		if (a.Vendor == DefaultVendor) != (b.Vendor == DefaultVendor) {
			return a.Vendor == DefaultVendor
		}
		return a.Vendor < b.Vendor
	})
}

// resolveDownload follows a stable link to the real file, and reports the size
// and the final URL. Only the response headers are read.
func resolveDownload(ctx context.Context, url string) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", userAgent())
	client := &http.Client{
		// Stop at the first response: the body of a JDK archive is not needed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	final := resp.Request.URL.String()
	size := sizeFromResponse(resp)
	// Follow the hops by hand so a redirect chain still lands on the file.
	for i := 0; i < 5 && resp.StatusCode >= 300 && resp.StatusCode < 400; i++ {
		loc := resp.Header.Get("Location")
		if loc == "" {
			break
		}
		next := resolveRelative(final, loc)
		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return "", 0, err
		}
		req2.Header.Set("Range", "bytes=0-0")
		req2.Header.Set("User-Agent", userAgent())
		resp2, err := client.Do(req2)
		if err != nil {
			return "", 0, err
		}
		resp.Body.Close()
		resp = resp2
		final = resp.Request.URL.String()
		if s := sizeFromResponse(resp); s > 0 {
			size = s
		}
	}
	if resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	return final, size, nil
}

// sizeFromResponse reads the archive size. A ranged request answers 206 with
// Content-Length: 1, so the total has to come from Content-Range.
func sizeFromResponse(resp *http.Response) int64 {
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
				return n
			}
		}
	}
	if resp.StatusCode == http.StatusPartialContent {
		return 0
	}
	return resp.ContentLength
}

// resolveRelative resolves a Location header that may be relative.
func resolveRelative(base, loc string) string {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc
	}
	if strings.HasPrefix(loc, "/") {
		if i := strings.Index(base, "://"); i > 0 {
			rest := base[i+3:]
			if j := strings.IndexByte(rest, '/'); j >= 0 {
				return base[:i+3+j] + loc
			}
			return base + loc
		}
	}
	if i := strings.LastIndexByte(base, '/'); i > 0 {
		return base[:i+1] + loc
	}
	return base + "/" + loc
}

// archiveName takes the file name out of a URL, ignoring any query string.
func archiveName(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndexAny(u, "/\\"); i >= 0 {
		u = u[i+1:]
	}
	return u
}

// numericVersion joins a version array as major.minor.patch, so 21.0.13.0 and
// 21.0.13 name the same release.
func numericVersion(parts []int) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	for len(parts) < 3 {
		parts = append(parts, 0)
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.Itoa(p))
	}
	return b.String()
}

// normalizeJavaVersion keeps major.minor.patch and drops build metadata, so
// versions from different vendors line up in one row.
//
// Java 8 is spelled four different ways by these vendors, which all have to
// mean the same release:
//
//	Temurin    8.0.472
//	Zulu       8.0.472
//	Corretto   8.512.09.1   (the update number is the second field)
//	Liberica   8u504+1
func normalizeJavaVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// 8u504+1 → 8.0.504
	if m := javaURe.FindStringSubmatch(v); m != nil {
		update, _ := strconv.Atoi(m[2])
		return numericVersion([]int{8, 0, update})
	}
	// 1.8.0_412 → 8.0.412
	if m := javaOneDotEightRe.FindStringSubmatch(v); m != nil {
		update, _ := strconv.Atoi(m[1])
		return numericVersion([]int{8, 0, update})
	}
	// Drop build metadata such as +11 or -LTS before splitting.
	if i := strings.IndexAny(v, "_+-"); i > 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return v
	}
	// Corretto spells Java 8 as 8.<update>.<patch>.<build>, so a three-digit
	// second field is an update number, not a minor version.
	if len(nums) >= 2 && nums[0] == 8 && nums[1] >= 100 {
		return numericVersion([]int{8, 0, nums[1]})
	}
	if len(nums) > 3 {
		nums = nums[:3]
	}
	// A vendor that only names the major still means <major>.0.0, so it lines
	// up with the same release reported by a vendor that names the minor.
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	return numericVersion(nums)
}

var (
	javaURe           = regexp.MustCompile(`^(\d+)u(\d+)`)
	javaOneDotEightRe = regexp.MustCompile(`^1\.8\.\d+_(\d+)`)
)

// versionParts parses a dotted version into integers.
func versionParts(v string) []int {
	parts := strings.Split(normalizeJavaVersion(v), ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}

// compareNumeric compares two version arrays element by element.
func compareNumeric(a, b []int) int {
	n := int(math.Max(float64(len(a)), float64(len(b))))
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func adoptiumOS(goos string) string {
	return map[string]string{"windows": "windows", "darwin": "mac", "linux": "linux"}[goos]
}

func adoptiumArch(arch string) string {
	return map[string]string{"x64": "x64", "arm64": "aarch64", "386": "x86"}[arch]
}

// isJavaLTS reports whether a Java major is a long-term-support release.
func isJavaLTS(major int) bool {
	switch major {
	case 8, 11, 17, 21, 25:
		return true
	}
	return false
}

// uniqueStrings drops duplicate messages while keeping the order.
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
