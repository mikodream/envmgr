package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReadyPreflightsMissingSDKsBeforeInstalling(t *testing.T) {
	installs := 0
	_, err := readyProject(context.Background(), Project{}, nil, readyOperations{
		resolve: func(Project) []Resolution {
			return []Resolution{{Kind: Node, Expr: "20", Missing: true}, {Kind: Ruby, Expr: "3.3", Missing: true}}
		},
		list:    func(context.Context, Kind) ([]Remote, error) { return []Remote{{Kind: Node, Version: "20.1.0"}}, nil },
		install: func(context.Context, Remote, Progress) (Installed, error) { installs++; return Installed{}, nil },
	})
	if err == nil || installs != 0 {
		t.Fatalf("preflight err=%v installs=%d", err, installs)
	}
}

func TestMatchingRemoteSelectsVersionAndDefaultVendor(t *testing.T) {
	list := []Remote{{Kind: Java, Version: "21.0.1", Vendor: "zulu", LTS: true}, {Kind: Java, Version: "21.0.1", Vendor: DefaultVendor, LTS: true}, {Kind: Java, Version: "25.0.1", Vendor: DefaultVendor}}
	for _, tc := range []struct{ expr, version string }{{"21", "21.0.1"}, {"lts", "21.0.1"}, {">=21 <26", "25.0.1"}} {
		got, err := matchingRemote(list, Java, tc.expr)
		if err != nil || got.Version != tc.version || got.Vendor != DefaultVendor {
			t.Fatalf("%s: %+v %v", tc.expr, got, err)
		}
	}
	if _, err := matchingRemote(list, Java, "17"); err == nil {
		t.Fatal("missing match accepted")
	}
}

func TestReadyCancellationStopsBeforeNextSDK(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	installs := 0
	_, err := readyProject(ctx, Project{}, nil, readyOperations{
		resolve: func(Project) []Resolution {
			return []Resolution{{Kind: Node, Expr: "20", Missing: true}, {Kind: Python, Expr: "3", Missing: true}}
		},
		list: func(_ context.Context, k Kind) ([]Remote, error) {
			version := "20.0.0"
			if k == Python {
				version = "3.12.0"
			}
			return []Remote{{Kind: k, Version: version}}, nil
		},
		install: func(context.Context, Remote, Progress) (Installed, error) {
			installs++
			cancel()
			return Installed{}, context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) || installs != 1 {
		t.Fatalf("err=%v installs=%d", err, installs)
	}
}

func sdkArchive(t *testing.T, body string) []byte {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	h := &zip.FileHeader{Name: "sdk/bin/node", Method: zip.Deflate}
	h.SetMode(0755)
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestReadyDownloadsInstallsAndVerifiesExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture executable")
	}
	data := sdkArchive(t, "#!/bin/sh\nprintf 'v20.12.0\\n'\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	sum := sha256.Sum256(data)
	remote := Remote{Kind: Node, Version: "20.12.0", File: "node.zip", URL: server.URL + "/node.zip", SHA256: hex.EncodeToString(sum[:])}
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	project := Project{Path: t.TempDir()}
	var installed *Installed
	var stages []string
	result, err := readyProject(context.Background(), project, func(e ReadyEvent) {
		if len(stages) == 0 || stages[len(stages)-1] != e.Stage {
			stages = append(stages, e.Stage)
		}
	}, readyOperations{
		resolve: func(Project) []Resolution {
			return []Resolution{{Kind: Node, Expr: "20", Missing: installed == nil, Installed: installed}}
		},
		list: func(context.Context, Kind) ([]Remote, error) { return []Remote{remote}, nil },
		install: func(ctx context.Context, r Remote, p Progress) (Installed, error) {
			i, e := installArchive(ctx, cfg, Node, r, p, nil)
			if e == nil {
				installed = &i
			}
			return i, e
		},
		verify: verifyProjectSDK,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 1 || len(result.Verified) != 1 || result.Verified[0].Version != "20.12.0" {
		t.Fatalf("result=%+v", result)
	}
	want := []string{"检查版本", "下载", "校验", "解包", "安装", "验证"}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("stages=%v", stages)
	}
}

func TestReadyRejectsExecutableReportingWrongVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture executable")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "node"), []byte("#!/bin/sh\necho v22.1.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	inst := ScanInstall(Installed{Kind: Node, Version: "20.12.0", Path: root})
	_, err := verifyProjectSDK(context.Background(), Project{Path: root}, Resolution{Kind: Node, Installed: &inst}, Env{})
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("err=%v", err)
	}
}

func TestInstallCancellationPreservesPreviousSDK(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	r := Remote{Kind: Node, Version: "20.12.0", File: "node.zip"}
	cache, err := CacheDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, r.File), sdkArchive(t, "replacement"), 0644); err != nil {
		t.Fatal(err)
	}
	dest, _ := installPath(cfg, Node, r.Version)
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "previous")
	if err := os.WriteFile(marker, []byte("working"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = installArchive(ctx, cfg, Node, r, nil, func(string) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "working" {
		t.Fatalf("previous SDK lost: %q %v", data, err)
	}
	temps, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".install-*"))
	if len(temps) > 0 {
		t.Fatalf("temporary directories remain: %v", temps)
	}
}

func TestIncompleteEnvironmentBlocksDirectExecutionAndTerminal(t *testing.T) {
	env := BuildEnv([]Resolution{{Kind: Node, Expr: "99", Missing: true}})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunCommand(t.TempDir(), env, []string{exe, "-test.run=^$"}); err == nil {
		t.Fatal("started incomplete environment")
	}
	if err := LaunchTerminal(t.TempDir(), env, Terminal{Path: exe}); err == nil {
		t.Fatal("opened incomplete terminal")
	}
	for _, sh := range Shells {
		if env.Export(sh) != "" {
			t.Fatalf("partial export for %s", sh)
		}
	}
}

func TestSDKVersionOutputFormats(t *testing.T) {
	for _, tc := range []struct {
		kind         Kind
		output, want string
	}{
		{Go, "go version go1.27.3 darwin/arm64", "1.27.3"},
		{Java, `java version "1.8.0_472"`, "8.0.472"},
		{Java, `openjdk version "21.0.9" 2025-10-21 LTS`, "21.0.9"},
		{Rust, "cargo 1.90.0 (840b83a10 2025-07-30)", "1.90.0"},
	} {
		if got := sdkVersionOutput(tc.kind, tc.output); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestCatalogCancellationDoesNotPoisonRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			cancel()
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.NodeMirror = server.URL
	catalog := NewCatalog()
	if _, err := catalog.List(ctx, Node, cfg, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	if _, err := catalog.List(context.Background(), Node, cfg, false); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("retry reused cancelled request: %d requests", requests.Load())
	}
}

func TestPublishingFailureRestoresPreviousInstallation(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "installed")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "working"), []byte("previous"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := publishInstallDirectory(filepath.Join(root, "missing-replacement"), dest); err == nil {
		t.Fatal("missing replacement accepted")
	}
	if b, err := os.ReadFile(filepath.Join(dest, "working")); err != nil || string(b) != "previous" {
		t.Fatalf("previous installation lost: %q %v", b, err)
	}
}

func TestVerifyInstalledGoExecutable(t *testing.T) {
	version := strings.TrimPrefix(runtime.Version(), "go")
	inst := ScanInstall(Installed{Kind: Go, Version: version, Path: runtime.GOROOT()})
	if inst.ExePath() == "" {
		t.Skip("Go executable absent in build environment")
	}
	got, err := verifyProjectSDK(context.Background(), Project{Path: t.TempDir()}, Resolution{Kind: Go, Installed: &inst}, BuildEnv([]Resolution{{Kind: Go, Installed: &inst}}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != version {
		t.Fatalf("actual=%q expected=%q", got.Version, version)
	}
}
