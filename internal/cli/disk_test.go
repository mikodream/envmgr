package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"envmgr/internal/core"
)

// writeTestFile creates a file with the given payload size.
func writeTestFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The disk command has to name what is installed, what is cached, and what a
// clean-up would free, then actually free it.
func TestDiskCommandReportsAndCleans(t *testing.T) {
	svc, _ := environmentCLI(t)
	home := os.Getenv("HOME")

	install := filepath.Join(home, ".envmgr", "sdk", "go", "1.24.0")
	writeTestFile(t, filepath.Join(install, "bin", "go"), 8*1024)
	if err := os.WriteFile(filepath.Join(install, "VERSION"), []byte("go1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache, err := core.CacheDir(svc.Config())
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "go1.23.0.darwin-arm64.tar.gz")
	writeTestFile(t, archive, 64*1024)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(archive, old, old); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(home, ".envmgr", "sdk", "go", ".install-12")
	writeTestFile(t, filepath.Join(residue, "partial"), 2048)
	if err := os.Chtimes(residue, old, old); err != nil {
		t.Fatal(err)
	}
	svc.Refresh()

	var code int
	out := captureCLIOutput(t, func() { code = cmdDisk(svc, nil) })
	if code != 0 {
		t.Fatalf("disk exit=%d", code)
	}
	for _, want := range []string{"已装 SDK", "1.24.0", "下载缓存", "64.0 KB", "残留", "可释放"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}

	clean := captureCLIOutput(t, func() { code = cmdDisk(svc, []string{"--clean"}) })
	if code != 0 {
		t.Fatalf("clean exit=%d", code)
	}
	if !strings.Contains(clean, "释放") {
		t.Fatalf("clean output = %q", clean)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("cache archive survived: %v", err)
	}
	if _, err := os.Stat(residue); !os.IsNotExist(err) {
		t.Fatalf("residue survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(install, "bin", "go")); err != nil {
		t.Fatalf("installed version was deleted: %v", err)
	}
}

// --json exists so a script can read the numbers instead of scraping them.
func TestDiskCommandJSON(t *testing.T) {
	svc, _ := environmentCLI(t)
	home := os.Getenv("HOME")
	install := filepath.Join(home, ".envmgr", "sdk", "go", "1.24.0")
	writeTestFile(t, filepath.Join(install, "bin", "go"), 4096)
	if err := os.WriteFile(filepath.Join(install, "VERSION"), []byte("go1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.Refresh()

	var code int
	out := captureCLIOutput(t, func() { code = cmdDisk(svc, []string{"--json"}) })
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var rep core.DiskReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	found := false
	for _, e := range rep.Entries {
		if e.Kind == core.Go && e.Version == "1.24.0" {
			found = true
			if e.Size < 4096 {
				t.Fatalf("size = %d; want at least 4096", e.Size)
			}
		}
	}
	if !found {
		t.Fatalf("json report has no go@1.24.0: %+v", rep.Entries)
	}
}
