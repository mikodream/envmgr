package core

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	digestA = "cf1bc4d6be33e4f739d97eb8b873f3a08bd6562cf4652db771acb82f8f4b7113"
	digestB = "73f1ed14486aa278d331e95f7d6d452f5cfb69d90b418e26e62fd4846d53557d"
)

func TestDigestFromDocument(t *testing.T) {
	sums := digestA + "  cpython-3.12.4+20240604-aarch64-apple-darwin-install_only.tar.gz\n" +
		digestB + " *node-v20.11.1-darwin-arm64.tar.gz\n"
	cases := []struct {
		name string
		body string
		file string
		url  string
		want string
	}{
		{
			name: "sums listing picks the matching file",
			body: sums,
			file: "node-v20.11.1-darwin-arm64.tar.gz",
			want: digestB,
		},
		{
			name: "sums listing ignores other files",
			body: sums,
			file: "python-3.13.0.tar.gz",
			want: "",
		},
		{
			name: "bare digest",
			body: digestA + "\n",
			want: digestA,
		},
		{
			name: "azul record describes this build",
			body: `{"sha256_hash":"` + digestA + `","download_url":"https://cdn.example/zulu.zip"}`,
			url:  "https://cdn.example/zulu.zip",
			want: digestA,
		},
		{
			name: "azul record of another build is ignored",
			body: `{"sha256_hash":"` + digestA + `","download_url":"https://cdn.example/other.zip"}`,
			url:  "https://cdn.example/zulu.zip",
			want: "",
		},
		{
			name: "html error page",
			body: "<html><body>Not Found</body></html>",
			want: "",
		},
		{
			name: "truncated digest",
			body: digestA[:40],
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := digestFromDocument([]byte(tc.body), tc.file, tc.url); got != tc.want {
				t.Fatalf("digest=%q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveChecksumReadsDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(digestA + "  node.zip\n"))
	}))
	defer server.Close()

	r := resolveChecksum(context.Background(), Remote{
		File: "node.zip", URL: server.URL + "/node.zip",
		ChecksumURL: server.URL + "/SHASUMS256.txt",
	})
	if r.SHA256 != digestA {
		t.Fatalf("sha256=%q", r.SHA256)
	}
	// A digest already known is never overwritten.
	kept := resolveChecksum(context.Background(), Remote{SHA256: digestB, ChecksumURL: server.URL})
	if kept.SHA256 != digestB {
		t.Fatalf("sha256=%q", kept.SHA256)
	}
}

func TestResolveChecksumToleratesMissingDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	r := resolveChecksum(context.Background(), Remote{File: "node.zip", ChecksumURL: server.URL + "/missing"})
	if r.SHA256 != "" {
		t.Fatalf("unexpected digest %q", r.SHA256)
	}
}

func TestInstallRejectsBadSumFile(t *testing.T) {
	if testing.Short() {
		t.Skip("network-free fixture still unpacks a zip")
	}
	data := sdkArchive(t, "#!/bin/sh\nprintf 'v20.12.0\\n'\n")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])
	bodies := map[string]string{
		"/ok/SHASUMS256.txt":  good + "  node.zip\n",
		"/bad/SHASUMS256.txt": digestA + "  node.zip\n",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := bodies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	remote := func(prefix string) Remote {
		return Remote{
			Kind: Node, Version: "20.12.0", File: "node.zip",
			URL: server.URL + prefix + "/node.zip", ChecksumURL: server.URL + prefix + "/SHASUMS256.txt",
		}
	}
	if _, err := installArchive(context.Background(), cfg, Node, remote("/ok"), nil, nil); err != nil {
		t.Fatalf("good digest: %v", err)
	}

	_, err := installArchive(context.Background(), cfg, Node, remote("/bad"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("want checksum error, got %v", err)
	}
	// The corrupt archive must not stay in the cache: retrying would reuse it.
	cache, err := CacheDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(cache, "node.zip")); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt archive left in cache: %v", statErr)
	}
}

func TestVerifySHA1AcceptsBellSoftDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liberica.tar.gz")
	body := []byte("not really an archive")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(body)
	if err := VerifySHA1(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("matching digest: %v", err)
	}
	if err := VerifySHA1(path, hex.EncodeToString(make([]byte, 20))); err == nil {
		t.Fatal("mismatched digest accepted")
	}
}

func TestCorrettoArchiveTypeByOS(t *testing.T) {
	// Corretto answers 404 for a zip on macOS and Linux; only Windows has one.
	for goos, want := range map[string]string{"windows": "zip", "darwin": "tar.gz", "linux": "tar.gz"} {
		if got := correttoExt(goos); got != want {
			t.Fatalf("%s: ext=%q want %q", goos, got, want)
		}
	}
}
