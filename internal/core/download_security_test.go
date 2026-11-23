package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type securityArchiveEntry struct {
	name string
	body string
	link string
	dir  bool
}

func writeSecurityArchive(t *testing.T, format string, entries ...securityArchiveEntry) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "test."+format)
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if format == "zip" {
		w := zip.NewWriter(f)
		for _, entry := range entries {
			h := &zip.FileHeader{Name: entry.name}
			h.SetMode(0o755)
			if entry.dir {
				h.Name += "/"
				h.SetMode(os.ModeDir | 0o755)
			}
			out, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := out.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(f)
		w := tar.NewWriter(gz)
		for _, entry := range entries {
			h := &tar.Header{Name: entry.name, Mode: 0o755, Typeflag: tar.TypeReg, Size: int64(len(entry.body))}
			if entry.link != "" {
				h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, entry.link, 0
			} else if entry.dir {
				h.Typeflag, h.Size = tar.TypeDir, 0
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Typeflag == tar.TypeReg {
				if _, err := w.Write([]byte(entry.body)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func securitySymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlink support required: %v", err)
	}
}

func TestExtractSecurityRejectsEscapingTarSymlink(t *testing.T) {
	for _, tc := range []struct{ name, link string }{
		{"link", "../outside"},
		{"sdk/link", "../../outside"},
		{"sdk/link", "/tmp/outside"},
	} {
		t.Run(tc.name+"_"+tc.link, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out")
			archive := writeSecurityArchive(t, "tar.gz", securityArchiveEntry{name: tc.name, link: tc.link})
			if err := Extract(archive, dst); err == nil {
				t.Fatal("accepted an escaping symlink")
			}
			if _, err := os.Lstat(filepath.Join(dst, tc.name)); !os.IsNotExist(err) {
				t.Fatalf("escaping symlink was created: %v", err)
			}
		})
	}
}

func TestExtractSecurityRejectsWritesThroughExistingSymlink(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		for _, kind := range []string{"file", "directory", "directory entry", "dangling file"} {
			t.Run(format+"/"+kind, func(t *testing.T) {
				base := t.TempDir()
				dst, outside := filepath.Join(base, "out"), filepath.Join(base, "outside")
				for _, dir := range []string{dst, outside} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				victim := filepath.Join(outside, "victim")
				if kind != "dangling file" {
					if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				entry := securityArchiveEntry{name: "link", body: "overwritten"}
				link := filepath.Join("..", "outside", "victim")
				if kind == "directory" || kind == "directory entry" {
					link = filepath.Join("..", "outside")
					entry.name = "link/victim"
					if kind == "directory entry" {
						entry.name, entry.dir, entry.body = "link/newdir", true, ""
					}
				}
				securitySymlink(t, link, filepath.Join(dst, "link"))
				if err := Extract(writeSecurityArchive(t, format, entry), dst); err == nil {
					t.Fatal("accepted a write through an escaping symlink")
				}
				got, err := os.ReadFile(victim)
				if kind == "dangling file" {
					if !os.IsNotExist(err) {
						t.Fatalf("created outside file: %q, %v", got, err)
					}
				} else if err != nil || string(got) != "original" {
					t.Fatalf("outside file changed: %q, %v", got, err)
				}
				if _, err := os.Stat(filepath.Join(outside, "newdir")); !os.IsNotExist(err) {
					t.Fatalf("created outside directory: %v", err)
				}
			})
		}
	}
}

func TestExtractSecurityTarArchiveSymlinkThenFile(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "out")
	victim := filepath.Join(base, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := writeSecurityArchive(t, "tar.gz",
		securityArchiveEntry{name: "link", link: ".."},
		securityArchiveEntry{name: "link/victim", body: "overwritten"},
	)
	if err := Extract(archive, dst); err == nil {
		t.Fatal("accepted archive symlink escape")
	}
	got, err := os.ReadFile(victim)
	if err != nil || string(got) != "original" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestExtractSecurityPreservesSDKRelativeSymlinks(t *testing.T) {
	for _, linksFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "files first", true: "links first"}[linksFirst], func(t *testing.T) {
			entries := []securityArchiveEntry{
				{name: "sdk/lib/tool", body: "sdk executable"},
				{name: "sdk/bin/tool", link: "../lib/tool"},
				{name: "sdk/bin/alias", link: "tool"},
			}
			if linksFirst {
				entries = append(entries[1:], entries[0])
			}
			dst := filepath.Join(t.TempDir(), "out")
			if err := Extract(writeSecurityArchive(t, "tar.gz", entries...), dst); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"tool", "alias"} {
				got, err := os.ReadFile(filepath.Join(dst, "sdk", "bin", name))
				if err != nil || string(got) != "sdk executable" {
					t.Fatalf("SDK link %s: %q, %v", name, got, err)
				}
			}
			if link, err := os.Readlink(filepath.Join(dst, "sdk", "bin", "tool")); err != nil || link != "../lib/tool" {
				t.Fatalf("relative SDK symlink changed: %q, %v", link, err)
			}
		})
	}
}

func TestExtractSecurityRejectsSymlinkResolutionEscape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []securityArchiveEntry
	}{
		{"symlink parent", []securityArchiveEntry{
			{name: "sdk/alias", link: ".."},
			{name: "sdk/alias/escape", link: "../outside"},
		}},
		{"symlink target", []securityArchiveEntry{
			{name: "sdk/alias", link: ".."},
			{name: "escape", link: "sdk/alias/../outside"},
		}},
		{"later symlink target", []securityArchiveEntry{
			{name: "escape", link: "sdk/alias/../outside"},
			{name: "sdk/alias", link: ".."},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out")
			if err := Extract(writeSecurityArchive(t, "tar.gz", tc.entries...), dst); err == nil {
				t.Fatal("accepted a symlink chain escaping after actual resolution")
			}
			if _, err := os.Lstat(filepath.Join(dst, "escape")); !os.IsNotExist(err) {
				t.Fatalf("escaping symlink remains: %v", err)
			}
		})
	}
}

func TestExtractContextCanceled(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			archive := writeSecurityArchive(t, format, securityArchiveEntry{name: "file", body: "data"})
			dst := filepath.Join(t.TempDir(), "out")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := ExtractContext(ctx, archive, dst); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatalf("canceled extraction created destination: %v", err)
			}
		})
	}
}

type cancelingArchiveReader struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelingArchiveReader) Read(p []byte) (int, error) {
	r.reads++
	r.cancel()
	return copy(p, "data"), nil
}

func TestArchiveContextReaderCancelsDuringCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelingArchiveReader{cancel: cancel}
	_, err := io.Copy(io.Discard, archiveContextReader{ctx: ctx, r: source})
	if !errors.Is(err, context.Canceled) || source.reads != 1 {
		t.Fatalf("copy did not stop after cancellation: reads=%d, err=%v", source.reads, err)
	}
}

func TestExtractSecurityRejectsTraversal(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		for _, name := range []string{"../victim", "sdk/../../victim", "/absolute/victim"} {
			t.Run(format+"/"+name, func(t *testing.T) {
				if err := Extract(writeSecurityArchive(t, format, securityArchiveEntry{name: name, body: "bad"}), filepath.Join(t.TempDir(), "out")); err == nil {
					t.Fatal("accepted traversal path")
				}
			})
		}
	}
}
