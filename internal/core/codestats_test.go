package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeCodeStatsFile(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodeStatsLines(t *testing.T) {
	for _, tt := range []struct {
		name, text  string
		code, blank int
	}{
		{"empty", "", 0, 0},
		{"no newline", "package main", 1, 0},
		{"trailing newline", "package main\n", 1, 0},
		{"one blank", "\n", 0, 1},
		{"multiple blank", "\n\n", 0, 2},
		{"crlf and whitespace", "// comment\r\n \t\r\ncode\r\n\r\n", 2, 2},
		{"last blank without newline", "code\n \t", 1, 1},
		{"unicode whitespace", "\u2003\n标签\n", 1, 1},
		{"comments count", "/*\n * comment\n */\n", 3, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, blank := codeStatsLines([]byte(tt.text))
			if code != tt.code || blank != tt.blank {
				t.Fatalf("lines = %d/%d, want %d/%d", code, blank, tt.code, tt.blank)
			}
		})
	}
}

func TestLoadCodeStatsSourcesAndExclusions(t *testing.T) {
	dir := t.TempDir()
	sources := map[string]string{
		"main.go":      "// comment\r\npackage main\r\n \t\r\n",
		"web/app.tsx":  "// comment\nconst label = '非空行';\n",
		"web/empty.ts": "",
		"script.py":    "print('hi')\n\n",
		"web/page.vue": "<template />",
	}
	var size int64
	for path, text := range sources {
		writeCodeStatsFile(t, dir, path, text)
		size += int64(len(text))
	}
	for _, path := range []string{"README.md", "package.json", "config.yaml", "config.yml", "unknown.xyz", "image.png"} {
		writeCodeStatsFile(t, dir, path, "not source\n")
	}
	for _, dependency := range []string{".git", "node_modules", "vendor", ".venv", "venv", "dist", "build", "target", "__pycache__", "coverage", ".next", ".nuxt", ".output", ".svelte-kit", "bin", "obj"} {
		writeCodeStatsFile(t, dir, dependency+"/excluded.go", "excluded\n")
	}
	writeCodeStatsFile(t, dir, "binary.go", "source\x00binary\n")
	writeCodeStatsFile(t, dir, "encoding.py", "\xff\xfe")
	writeCodeStatsFile(t, dir, "large.ts", strings.Repeat("a", codeStatsMaxFileBytes+1))
	stats := LoadCodeStats(context.Background(), dir)
	if stats.Err != "" || stats.Truncated {
		t.Fatalf("scan = %+v", stats)
	}
	if stats.Files != 5 || stats.CodeLines != 6 || stats.BlankLines != 2 || stats.Bytes != size {
		t.Fatalf("totals = %+v, bytes want %d", stats, size)
	}
	if stats.Skipped != 25 { // 6 unknown/docs/config, 16 directories, 3 invalid/large.
		t.Fatalf("skipped = %d, want 25", stats.Skipped)
	}
	want := []LanguageStats{
		{Language: "Go", Files: 1, CodeLines: 2, BlankLines: 1, Bytes: int64(len(sources["main.go"]))},
		{Language: "TypeScript", Files: 2, CodeLines: 2, Bytes: int64(len(sources["web/app.tsx"]))},
		{Language: "Python", Files: 1, CodeLines: 1, BlankLines: 1, Bytes: int64(len(sources["script.py"]))},
		{Language: "Vue", Files: 1, CodeLines: 1, Bytes: int64(len(sources["web/page.vue"]))},
	}
	if !reflect.DeepEqual(stats.Languages, want) {
		t.Fatalf("languages = %+v, want %+v", stats.Languages, want)
	}
}

func TestLoadCodeStatsGitIgnoresAndProjectScope(t *testing.T) {
	dir, _ := tempRepo(t)
	if out, err := runGit(dir, "init"); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	writeCodeStatsFile(t, dir, ".gitignore", "ignored.go\nignored/\n*.tmp.ts\nnode_modules/\n")
	for _, path := range []string{"outside.go", "app/main.go", "app/ignored.go", "app/ignored/hidden.go", "app/skip.tmp.ts", "app/tracked.tmp.ts", "app/node_modules/tracked.js", "app/vendor/tracked.go", "app/space name.ts", "app/.gitignore"} {
		writeCodeStatsFile(t, dir, path, "line\n")
	}
	// An ignored tracked source remains project code; dependencies are excluded
	// even when forced into the index. No commits or identity changes are needed.
	if out, err := runGit(dir, "add", "-f", "app/tracked.tmp.ts", "app/node_modules/tracked.js", "app/vendor/tracked.go"); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
	stats := LoadCodeStats(context.Background(), filepath.Join(dir, "app"))
	if stats.Err != "" || stats.Truncated || stats.Files != 3 || stats.CodeLines != 3 || stats.Bytes != 15 {
		t.Fatalf("scoped Git scan = %+v", stats)
	}
	if len(stats.Languages) != 2 || stats.Languages[0].Language != "TypeScript" || stats.Languages[0].Files != 2 {
		t.Fatalf("languages = %+v", stats.Languages)
	}
	// A hostile ambient repository location must not defeat explicit -C scope.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	again := LoadCodeStats(context.Background(), filepath.Join(dir, "app"))
	if !reflect.DeepEqual(again, stats) {
		t.Fatalf("ambient Git override changed result: %+v versus %+v", again, stats)
	}
}

func TestLoadCodeStatsMissingTrackedFileKeepsPartialResult(t *testing.T) {
	dir, _ := tempRepo(t)
	if _, err := runGit(dir, "init"); err != nil {
		t.Fatal(err)
	}
	writeCodeStatsFile(t, dir, "a.go", "package a\n")
	writeCodeStatsFile(t, dir, "z.go", "package z\n")
	if _, err := runGit(dir, "add", "a.go", "z.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "z.go")); err != nil {
		t.Fatal(err)
	}
	stats := LoadCodeStats(context.Background(), dir)
	if stats.Files != 1 || stats.CodeLines != 1 || stats.Skipped != 1 || stats.Err == "" || stats.Truncated {
		t.Fatalf("partial scan = %+v", stats)
	}
}

func TestLoadCodeStatsSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeCodeStatsFile(t, dir, "real.go", "package real\n")
	writeCodeStatsFile(t, outside, "outside.go", "outside\noutside\n")
	if err := os.Symlink(filepath.Join(outside, "outside.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real.go"), filepath.Join(dir, "internal-link.go")); err != nil {
		t.Fatal(err)
	}
	stats := LoadCodeStats(context.Background(), dir)
	if stats.Err != "" || stats.Files != 1 || stats.CodeLines != 1 || stats.Skipped != 3 {
		t.Fatalf("symlink scan = %+v", stats)
	}
	rootLink := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(dir, rootLink); err != nil {
		t.Fatal(err)
	}
	if got := LoadCodeStats(context.Background(), rootLink); got.Err == "" || got.Files != 0 {
		t.Fatalf("root symlink scan = %+v", got)
	}
}

func TestLoadCodeStatsCancellationAndBadDirectory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := LoadCodeStats(ctx, t.TempDir())
	if got.Err != context.Canceled.Error() || !got.Truncated || got.Files != 0 {
		t.Fatalf("cancelled = %+v", got)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	got = LoadCodeStats(ctx, t.TempDir())
	if got.Err != context.DeadlineExceeded.Error() || !got.Truncated {
		t.Fatalf("deadline = %+v", got)
	}
	got = LoadCodeStats(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if got.Err == "" || got.Truncated {
		t.Fatalf("missing directory = %+v", got)
	}
}

func TestLoadCodeStatsBudgets(t *testing.T) {
	for _, tt := range []struct {
		name   string
		limits codeStatsLimits
		files  int
	}{
		{"file count", codeStatsLimits{fileBytes: 100, readBytes: 100, files: 1, entries: 100}, 1},
		{"entries", codeStatsLimits{fileBytes: 100, readBytes: 100, files: 100, entries: 1}, 1},
		{"bytes", codeStatsLimits{fileBytes: 100, readBytes: 2, files: 100, entries: 100}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeCodeStatsFile(t, dir, "a.go", "a\n")
			writeCodeStatsFile(t, dir, "b.go", "b\n")
			got := loadCodeStats(context.Background(), dir, tt.limits)
			if got.Err != "" || !got.Truncated || got.Files != tt.files || got.CodeLines != tt.files {
				t.Fatalf("budgeted scan = %+v", got)
			}
		})
	}
}

func TestCodeStatsGitOutputBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &codeStatsOutput{limit: 5, cancel: cancel}
	// io.Copy exercises exactly the writer interface used by os/exec; embedding
	// bytes.Buffer would accidentally expose ReadFrom and bypass the byte cap.
	n, err := io.Copy(out, bytes.NewBufferString("abcdefgh"))
	if err == nil || n != 5 || out.buffer.Len() != 5 || !out.capped || ctx.Err() != context.Canceled {
		t.Fatalf("output budget: n=%d err=%v buffer=%q capped=%v ctx=%v", n, err, out.buffer.String(), out.capped, ctx.Err())
	}
}

func TestCodeStatsSourceAllowlist(t *testing.T) {
	for _, path := range []string{"a.go", "a.ts", "a.tsx", "a.js", "a.vue", "a.py", "a.java", "a.rs", "a.c", "a.cpp", "a.cs", "a.html", "a.css", "a.sh", "a.ps1", "a.sql", "a.HPP"} {
		if codeStatsLanguage(path) == "" {
			t.Errorf("missing source extension: %s", path)
		}
	}
	for _, path := range []string{"a.json", "a.yaml", "a.yml", "a.md", "a.txt", "go.mod", "go.sum", "package-lock.json", "image.png"} {
		if codeStatsLanguage(path) != "" {
			t.Errorf("non-source extension counted: %s", path)
		}
	}
}
