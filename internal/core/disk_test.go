package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// newDiskHome points HOME at an empty directory so the report reads and writes
// only throwaway state.
func newDiskHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	t.Setenv("HOME", home)
	return home
}

// writeFiller creates a file of the given size, so a measured footprint is a
// number the test knows.
func writeFiller(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The report separates what EnvMgr installed from what another manager owns,
// and sums both.
func TestDiskReportSplitsManagedAndExternal(t *testing.T) {
	home := newDiskHome(t)

	goDir := filepath.Join(home, ".envmgr", "sdk", "go", "1.24.0")
	writeFiller(t, filepath.Join(goDir, "VERSION"), 0)
	writeFiller(t, filepath.Join(goDir, "bin", "go"), 64*1024)
	if err := os.WriteFile(filepath.Join(goDir, "VERSION"), []byte("go1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pythonDir := filepath.Join(home, "vendor", "python")
	writeFiller(t, filepath.Join(pythonDir, "bin", "python3"), 32*1024)

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddManualInstall(Python, "3.12.0", pythonDir, ""); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.DiskReport()
	if err != nil {
		t.Fatal(err)
	}
	var gotGo, gotPy DiskEntry
	for _, e := range rep.Entries {
		switch e.Kind {
		case Go:
			gotGo = e
		case Python:
			gotPy = e
		}
	}
	if gotGo.Size < 64*1024 {
		t.Fatalf("go size = %d; want at least the 64 KiB payload", gotGo.Size)
	}
	if gotPy.Size < 32*1024 {
		t.Fatalf("python size = %d; want at least the 32 KiB payload", gotPy.Size)
	}
	// The machine running the test may have SDKs of its own, so the split is
	// checked against the two installs this test created rather than a total.
	if gotGo.Manager != "envmgr" {
		t.Fatalf("go manager = %q; want envmgr", gotGo.Manager)
	}
	if gotPy.Manager != ManualManager {
		t.Fatalf("python manager = %q; want %q", gotPy.Manager, ManualManager)
	}
	if rep.Managed < gotGo.Size {
		t.Fatalf("managed = %d; want at least the Go install %d", rep.Managed, gotGo.Size)
	}
	if rep.External < gotPy.Size {
		t.Fatalf("external = %d; want at least the manual Python %d", rep.External, gotPy.Size)
	}
	if rep.Total != rep.Managed+rep.External {
		t.Fatalf("total = %d; want %d", rep.Total, rep.Managed+rep.External)
	}
}

// Measuring a JDK is expensive, so the second report must reuse the cache the
// first one wrote.
func TestDiskReportCachesSizes(t *testing.T) {
	home := newDiskHome(t)
	dir := filepath.Join(home, ".envmgr", "sdk", "go", "1.24.0")
	writeFiller(t, filepath.Join(dir, "bin", "go"), 8*1024)
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("go1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.DiskReport()
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := SizeCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("size cache was not written: %v", err)
	}
	// The cache must be keyed by the install path, so a later report can reuse
	// the number instead of walking the tree again.
	b, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), normalizePathKey(dir)) {
		t.Fatalf("size cache does not mention %s: %s", dir, b)
	}
	second, err := svc.DiskReport()
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != first.Total {
		t.Fatalf("total changed from %d to %d with nothing on disk changed", first.Total, second.Total)
	}
}

// A directory left behind by an interrupted unpack is residue, and a directory
// EnvMgr does list is not.
func TestResidueFindsInterruptedInstall(t *testing.T) {
	home := newDiskHome(t)
	root := filepath.Join(home, ".envmgr", "sdk")
	writeFiller(t, filepath.Join(root, "node", "20.11.1", "bin", "node"), 4096)
	writeFiller(t, filepath.Join(root, "node", ".install-839201", "bin", "node"), 2048)
	writeFiller(t, filepath.Join(root, "node", "21.0.0-broken", "share", "doc"), 1024)

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.DiskReport()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, r := range rep.Residue {
		labels[filepath.Base(r.Path)] = r.Label
	}
	if _, ok := labels[".install-839201"]; !ok {
		t.Fatalf("residue = %+v; want the .install-* scratch directory", rep.Residue)
	}
	if labels[".install-839201"] != "中断安装的临时目录" {
		t.Fatalf("label = %q; want 中断安装的临时目录", labels[".install-839201"])
	}
	if _, ok := labels["21.0.0-broken"]; !ok {
		t.Fatalf("residue = %+v; want the unlistable version directory", rep.Residue)
	}
	if _, ok := labels["20.11.1"]; ok {
		t.Fatalf("a listed version was reported as residue: %+v", rep.Residue)
	}
	if rep.ResidueSize < 2048+1024 {
		t.Fatalf("residue size = %d; want at least 3072", rep.ResidueSize)
	}
}

// Clean-up must free the cache and the residue while leaving every installed
// version, and every file too fresh to be abandoned, alone.
func TestCleanDiskKeepsInstallsAndFreshFiles(t *testing.T) {
	home := newDiskHome(t)
	root := filepath.Join(home, ".envmgr", "sdk")
	keep := filepath.Join(root, "go", "1.24.0")
	writeFiller(t, filepath.Join(keep, "bin", "go"), 16*1024)
	if err := os.WriteFile(filepath.Join(keep, "VERSION"), []byte("go1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(root, "go", ".install-777")
	writeFiller(t, filepath.Join(residue, "leftover"), 4096)

	cache, err := CacheDir(Config{})
	if err != nil {
		t.Fatal(err)
	}
	oldArchive := filepath.Join(cache, "go1.23.0.darwin-arm64.tar.gz")
	freshArchive := filepath.Join(cache, "go1.24.1.darwin-arm64.tar.gz")
	writeFiller(t, oldArchive, 128*1024)
	writeFiller(t, freshArchive, 256*1024)

	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{oldArchive, residue} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.CleanDisk(CleanOptions{Cache: true, Residue: true})
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if _, err := os.Stat(oldArchive); !os.IsNotExist(err) {
		t.Fatalf("old archive survived: %v", err)
	}
	if _, err := os.Stat(residue); !os.IsNotExist(err) {
		t.Fatalf("residue survived: %v", err)
	}
	if _, err := os.Stat(freshArchive); err != nil {
		t.Fatalf("fresh archive was deleted, an install may be running: %v", err)
	}
	if _, err := os.Stat(filepath.Join(keep, "bin", "go")); err != nil {
		t.Fatalf("installed version was touched: %v", err)
	}
	if res.CacheFiles != 1 || res.CacheBytes != 128*1024 {
		t.Fatalf("cache result = %+v; want one 128 KiB archive", res)
	}
	if len(res.Residue) != 1 || res.ResidueBytes != 4096 {
		t.Fatalf("residue result = %+v; want one 4 KiB directory", res)
	}
	if len(res.Skipped) == 0 {
		t.Fatalf("skipped = %v; want the fresh archive listed", res.Skipped)
	}
}

// An archive another process is downloading right now must survive the clean.
func TestCleanDiskSkipsLockedArchive(t *testing.T) {
	newDiskHome(t)
	cache, err := CacheDir(Config{})
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "python-3.12.0.tar.gz")
	writeFiller(t, archive, 64*1024)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(archive, old, old); err != nil {
		t.Fatal(err)
	}

	held := flock.New(archive + ".lock")
	if err := held.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Unlock() }()

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.CleanDisk(CleanOptions{Cache: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("locked archive was deleted: %v", err)
	}
	if res.CacheFiles != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v; want the locked archive skipped", res)
	}
}
