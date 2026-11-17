// Command snap renders EnvMgr's views to PNG files without opening a window,
// so the interface can be reviewed and iterated offline.
//
//	go run ./cmd/snap -out shots
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"

	"envmgr/internal/app"
	"envmgr/internal/core"
)

func main() {
	out := flag.String("out", "shots", "directory for the PNG files")
	w := flag.Int("w", 1180, "width in DIPs")
	h := flag.Int("h", 780, "height in DIPs")
	scale := flag.Float64("scale", 2, "device pixels per DIP")
	which := flag.String("page", "all", "all, dashboard, projects, toolchains, downloads, doctor, settings")
	dialog := flag.String("dialog", "", "add: render the manual-add dialog on top of the toolchains page")
	dark := flag.Bool("dark", false, "render the dark appearance")
	demo := flag.Bool("demo", true, "seed demo projects in a temporary home")
	live := flag.Bool("live", false, "use this machine as it really is, no demo data")
	kind := flag.String("kind", "", "SDK tab to show, e.g. java")
	openProject := flag.Int("project", -1, "open this project index in the projects page")
	openProjectName := flag.String("project-name", "", "open the project with this directory name")
	gitEdit := flag.Bool("git-edit", false, "open the Git identity editor on the project page")
	gitBind := flag.Bool("git-bind", false, "open the remote-address binding editor on the project page")
	expandEmpty := flag.Bool("expand", false, "expand the unconfigured SDKs of an opened project")
	vendorDlg := flag.Bool("vendor", false, "open the download-source chooser on the downloads page")
	vendorVersion := flag.String("vendor-version", "21.0.11", "version whose download sources are shown")
	flag.Parse()

	if *live {
		*demo = false
	}
	if *demo {
		seedDemoHome()
	}

	svc, err := core.NewService()
	if err != nil {
		log.Fatalf("service: %v", err)
	}
	// Demo projects belong to the temporary home only. In live mode the real
	// store must not gain entries just because a screenshot was rendered.
	if *demo {
		if err := seedProjects(svc); err != nil {
			log.Fatalf("projects: %v", err)
		}
	}
	a := app.New(svc)
	// A render draws one frame, so a background Git read would not have landed
	// yet. Warm the cache first: the gallery shows each project's commit address
	// from it.
	for _, p := range svc.Projects() {
		_ = svc.Git(p.Path)
	}
	if *dark {
		a.SetThemeMode("dark")
	}
	// Every SDK gets a sample list, so a render shows each tab's own data.
	// Live mode must not use them: it exists to show what this machine really
	// sees, which is the difference that makes the screenshot trustworthy.
	if !*live {
		for _, k := range core.Kinds {
			a.SetRemote(k, sampleRemote(k))
		}
	}
	if *kind != "" {
		if k, ok := core.ParseKind(*kind); ok {
			a.SetToolchainKind(k)
			a.SetDownloadKind(k)
		}
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}

	pages := []struct {
		name string
		page int
	}{
		{"dashboard", app.PageDashboard},
		{"projects", app.PageProjects},
		{"toolchains", app.PageToolchains},
		{"downloads", app.PageDownloads},
		{"doctor", app.PageDoctor},
		{"settings", app.PageSettings},
	}
	for _, p := range pages {
		if *which != "all" && *which != p.name {
			continue
		}
		a.SetPage(p.page)
		if *dialog == "add" && p.page == app.PageToolchains {
			a.OpenAddDialog()
		}
		if p.page == app.PageProjects && *openProjectName != "" {
			if !a.OpenProjectByName(*openProjectName, *expandEmpty) {
				log.Printf("no project named %s", *openProjectName)
			}
		}
		if p.page == app.PageProjects && *openProject >= 0 {
			a.OpenProject(*openProject, *expandEmpty)
		}
		if p.page == app.PageProjects && *gitEdit {
			a.OpenGitEditor()
		}
		if p.page == app.PageProjects && *gitBind {
			a.OpenGitBinding()
		}
		// In live mode the download list comes from the vendors over the
		// network. Render once to start the fetch, then wait for it: capturing
		// the empty page would show the layout but none of the data.
		if *live && p.page == app.PageDownloads {
			ui.Render(a.View, *w, *h, float32(*scale))
			for i := 0; i < 300; i++ {
				if a.RemoteReady(a.DownloadsKind()) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		// The chooser needs the list, so it opens after the wait above.
		if p.page == app.PageDownloads && *vendorDlg {
			if !a.OpenVendorPicker(*vendorVersion) {
				log.Printf("no source chooser for %s", *vendorVersion)
			}
		}
		var img = ui.Render(a.View, *w, *h, float32(*scale))
		if *dark {
			tt := ui.NewTesterWith(a.View, *w, *h, ui.TesterOptions{Dark: true})
			tt.SetScale(float32(*scale))
			tt.Frame()
			img = tt.Image()
		}
		name := p.name
		if p.page == app.PageDownloads && *vendorDlg {
			name = "downloads-vendor"
		}
		if p.page == app.PageProjects && (*openProject >= 0 || *openProjectName != "") {
			name = "project-detail"
			if *openProjectName != "" {
				name = "git-" + *openProjectName
			}
			if *gitEdit {
				name += "-edit"
			}
			if *gitBind {
				name += "-bind"
			}
			if *expandEmpty {
				name = "project-detail-expanded"
			}
		}
		if *dark {
			name += "-dark"
		}
		// A live render never overwrites the deterministic sample shot: the
		// screenshots in the README must not depend on this machine's network.
		if *live && p.page == app.PageDownloads {
			name += "-live"
		}
		path := filepath.Join(*out, name+".png")
		f, err := os.Create(path)
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			log.Fatal(err)
		}
		f.Close()
		fi, _ := os.Stat(path)
		size := int64(0)
		if fi != nil {
			size = fi.Size()
		}
		fmt.Printf("%-28s %dx%d px  %d bytes\n", path, img.Rect.Dx(), img.Rect.Dy(), size)
	}
}

// sampleRemote is a believable download list for the screenshots, so the page
// renders without a network round trip. Each SDK gets its own versions, which
// is also what makes "the tab shows its own list" visible in a snapshot.
func sampleRemote(kind core.Kind) []core.Remote {
	type row struct {
		v      string
		label  string
		lts    bool
		size   int64
		vendor string
	}
	if kind == core.Java {
		// Java is a version × vendor matrix: the same version built by several
		// vendors, which is what the download page shows.
		matrix := []struct {
			version string
			lts     bool
			major   int
			vendors []string
		}{
			{"27.0.1", false, 27, []string{"corretto", "liberica"}},
			{"25.0.5", true, 25, []string{"temurin", "corretto", "liberica", "microsoft"}},
			{"21.0.11", true, 21, []string{"temurin", "corretto", "zulu", "liberica", "microsoft"}},
			{"17.0.17", true, 17, []string{"temurin", "corretto", "zulu", "liberica"}},
			{"11.0.29", true, 11, []string{"temurin", "corretto", "zulu", "microsoft"}},
			{"8.0.472", true, 8, []string{"temurin", "zulu", "liberica", "microsoft"}},
		}
		// Sizes differ a little per vendor, as they do in reality: the label is
		// the platform line, the vendor is its own field.
		sizeFor := map[string]int64{
			"temurin": 195 << 20, "corretto": 214 << 20, "zulu": 190 << 20,
			"liberica": 208 << 20, "microsoft": 210 << 20,
		}
		out := make([]core.Remote, 0, 24)
		for _, m := range matrix {
			for _, v := range m.vendors {
				out = append(out, core.Remote{
					Kind: core.Java, Version: m.version,
					Label: fmt.Sprintf("Java %d", m.major), LTS: m.lts,
					Size: sizeFor[v], Note: "2026-11-10",
					Vendor: v, VendorName: core.JavaVendorName(v),
				})
			}
		}
		return out
	}
	type plainRow struct {
		v     string
		label string
		lts   bool
		size  int64
	}
	var versions []plainRow
	switch kind {
	case core.Python:
		versions = []plainRow{
			{"3.14.3", "最新稳定", true, 26 << 20},
			{"3.13.9", "稳定", true, 25 << 20},
			{"3.12.11", "稳定", true, 24 << 20},
			{"3.11.14", "维护中", true, 23 << 20},
			{"3.10.19", "维护中", false, 22 << 20},
		}

	case core.Go:
		versions = []plainRow{
			{"1.27.4", "最新", false, 74 << 20},
			{"1.27.3", "稳定", false, 74 << 20},
			{"1.26.3", "稳定", false, 72 << 20},
			{"1.25.6", "稳定", true, 70 << 20},
		}
	case core.Rust:
		versions = []plainRow{
			{"1.98.0", "stable", false, 210 << 20},
			{"1.97.1", "stable", false, 208 << 20},
			{"1.96.0", "stable", false, 206 << 20},
		}
	case core.Ruby:
		versions = []plainRow{
			{"3.4.2", "最新", false, 22 << 20},
			{"3.3.6", "稳定", true, 21 << 20},
			{"3.2.9", "维护中", true, 20 << 20},
		}
	default:
		versions = []plainRow{
			{"24.13.0", "LTS · Krypton", true, 31 << 20},
			{"23.4.0", "Current", false, 30 << 20},
			{"22.14.0", "LTS · Jod", true, 29 << 20},
			{"20.19.0", "LTS · Iron", true, 28 << 20},
			{"18.20.5", "LTS · Hydrogen", true, 26 << 20},
			{"16.20.2", "归档", false, 24 << 20},
		}
	}
	out := make([]core.Remote, 0, len(versions))
	for _, v := range versions {
		out = append(out, core.Remote{
			Kind: kind, Version: v.v, Label: v.label, LTS: v.lts,
			Size: v.size, Note: "2026-11-10",
		})
	}
	return out
}

// seedDemoHome points the store at a temporary home, so a snapshot never
// touches the real one.
func seedDemoHome() {
	dir, err := os.MkdirTemp("", "envmgr-snap-*")
	if err != nil {
		log.Fatal(err)
	}
	if os.PathSeparator == '\\' {
		os.Setenv("USERPROFILE", dir)
	} else {
		os.Setenv("HOME", dir)
	}
}

// seedProjects creates a few directories with real marker files and adds them,
// so the screenshots show a populated list.
func seedProjects(svc *core.Service) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	demo := map[string]map[string]string{
		"orbit-web":   {".nvmrc": "24.13.0\n", ".python-version": "3.12.4\n"},
		"ledger-api":  {"go.mod": "module ledger\n\ngo 1.24\ntoolchain go1.25.6\n", ".java-version": "21.0.4\n"},
		"atlas-ml":    {".python-version": "3.11.9\n", "rust-toolchain.toml": "[toolchain]\nchannel = \"1.98.0\"\n"},
		"studio-app":  {".nvmrc": "18.16.0\n", ".ruby-version": "3.3.6\n"},
		"infra-tools": {".tool-versions": "nodejs 22.14.0\npython 3.14.3\n"},
	}
	for name, files := range demo {
		dir := filepath.Join(home, "code", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for file, body := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
				return err
			}
		}
		if _, err := svc.AddProject(dir); err != nil {
			return err
		}
	}
	// Give the demo projects the repository states the Git card has to render:
	// one with its own identity, one inheriting the global one, and the rest
	// plain directories.
	seedDemoRepos(home, svc)
	// Keep a stable order in the screenshots.
	time.Sleep(10 * time.Millisecond)
	_ = context.Background()
	return nil
}

// seedDemoRepos makes two of the demo projects real repositories. Everything
// happens in the temporary home, so no repository on the machine is touched.
func seedDemoRepos(home string, svc *core.Service) {
	// A demo global identity keeps the screenshot from showing whoever happens
	// to own this machine.
	globalCfg := filepath.Join(home, ".gitconfig-demo")
	body := "[user]\n\tname = Ada Lovelace\n\temail = ada@example.dev\n" +
		"[init]\n\tdefaultBranch = main\n"
	if os.WriteFile(globalCfg, []byte(body), 0o644) != nil {
		return
	}
	os.Setenv("GIT_CONFIG_GLOBAL", globalCfg)

	// ledger-api: a real repository with its own identity and one commit.
	ledger := filepath.Join(home, "code", "ledger-api")
	if runGitIn(ledger, "init") == nil {
		_ = runGitIn(ledger, "remote", "add", "origin", "git@github.com:ledger-team/ledger-api.git")
		_ = runGitIn(ledger, "config", "--local", "user.name", "Ledger Bot")
		_ = runGitIn(ledger, "config", "--local", "user.email", "bot@ledger.test")
		_ = runGitIn(ledger, "add", "-A")
		_ = runGitIn(ledger, "commit", "-m", "init")
	}
	// orbit-web: a repository that inherits the global identity. It is left with
	// one modified file and one untracked file, so the status line shows both
	// kinds of change at once.
	orbit := filepath.Join(home, "code", "orbit-web")
	if runGitIn(orbit, "init") == nil {
		_ = runGitIn(orbit, "remote", "add", "origin", "git@github.com:orbit-labs/orbit-web.git")
		_ = runGitIn(orbit, "add", "-A")
		_ = runGitIn(orbit, "commit", "-m", "init")
		// A committed file, changed afterwards.
		_ = os.WriteFile(filepath.Join(orbit, ".nvmrc"), []byte("24.13.0\n# bumped\n"), 0o644)
		// A file that has never been added.
		if f, err := os.OpenFile(filepath.Join(orbit, "app.js"), os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString("// untracked\n")
			f.Close()
		}
	}

	// studio-app: a repository whose remote has no binding, so the tile shows the
	// domain instead of a name — the fallback the user asked for.
	studio := filepath.Join(home, "code", "studio-app")
	if runGitIn(studio, "init") == nil {
		_ = runGitIn(studio, "remote", "add", "origin", "git@gitlab.com:studio/app.git")
		_ = runGitIn(studio, "add", "-A")
		_ = runGitIn(studio, "commit", "-m", "init")
	}

	// Two remote-address bindings, so the screenshots show the mechanism rather
	// than an empty list. One covers a whole organisation, the other one
	// repository, and both carry a name so the tiles show names rather than
	// addresses.
	for _, p := range []core.GitProfile{
		{Key: "github.com/orbit-labs", Label: "Orbit Labs 组织", Name: "Orbit Labs", Email: "dev@orbitlabs.example"},
		{Key: "github.com/ledger-team/ledger-api", Label: "记账项目", Name: "Ledger Bot", Email: "bot@ledger.test"},
	} {
		if err := svc.SaveGitProfile(p); err != nil {
			fmt.Fprintf(os.Stderr, "seed profile %s: %v\n", p.Key, err)
		}
	}
}

// runGitIn runs one git command in dir with output discarded.
func runGitIn(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd.Run()
}
