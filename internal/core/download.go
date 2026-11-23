package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Progress reports bytes received out of the total, -1 when unknown.
type Progress func(done, total int64)

// HTTPClient is the client every request uses. It follows the system proxy
// through the environment, and has a generous timeout because SDK archives
// are large.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute}
}

// FetchBytes downloads a small document, such as a version index.
func FetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := HTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// userAgent identifies EnvMgr to the servers it reads.
func userAgent() string {
	return "envmgr/1.0 (+https://github.com/egoist/mygo)"
}

// Download fetches url into dst, resuming a partial .part file when the
// server allows it, and reporting progress as it goes.
func Download(ctx context.Context, url, dst string, progress Progress) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := dst + ".part"
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent())
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0 // the server ignored the range: start over
	case http.StatusPartialContent:
	default:
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}

	total := resp.ContentLength
	if total > 0 {
		total += offset
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	done := offset
	if progress != nil {
		progress(done, total)
	}
	buf := make([]byte, 256<<10)
	var lastReport time.Time
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if progress != nil && time.Since(lastReport) > 80*time.Millisecond {
				lastReport = time.Now()
				progress(done, total)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if progress != nil {
		progress(done, total)
	}
	return os.Rename(part, dst)
}

// VerifySHA256 checks a file against an expected digest, ignoring case and an
// optional "sha256:" prefix. An empty want means "no check offered".
func VerifySHA256(path, want string) error {
	want = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(want), "sha256:")))
	if want == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("校验失败：期望 %s，实际 %s", want[:12], got[:12])
	}
	return nil
}

// Extract unpacks an archive into dst, choosing the format by extension.
func Extract(archive, dst string) error {
	return ExtractContext(context.Background(), archive, dst)
}

// ExtractContext unpacks an archive and stops when ctx is canceled.
func ExtractContext(ctx context.Context, archive, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	name := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(name, ".zip"):
		return extractZip(ctx, archive, dst)
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return extractTarGz(ctx, archive, dst)
	case strings.HasSuffix(name, ".tar.xz"):
		return errors.New("暂不支持 tar.xz 归档")
	default:
		return fmt.Errorf("未知归档格式：%s", filepath.Base(archive))
	}
}

// safeJoin resolves name inside dst, refusing paths that escape it.
func safeJoin(dst, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("归档包含非法路径：%s", name)
	}
	full := filepath.Join(dst, clean)
	rel, err := filepath.Rel(dst, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("归档包含非法路径：%s", name)
	}
	return full, nil
}

// safeArchiveLink resolves existing symlinks without cleaning away '..' first.
// Missing targets are allowed because SDK links often precede their files.
func safeArchiveLink(ctx context.Context, root *os.Root, name string) error {
	pending := strings.Split(filepath.ToSlash(name), "/")
	var parts []string
	links := 0
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return fmt.Errorf("归档包含非法链接：%s", name)
			}
			parts = parts[:len(parts)-1]
			continue
		}
		candidate := filepath.Join(filepath.Join(parts...), part)
		st, err := root.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			parts = append(parts, part)
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			parts = append(parts, part)
			continue
		}
		links++
		if links > 255 {
			return fmt.Errorf("归档链接循环：%s", name)
		}
		link, err := root.Readlink(candidate)
		if err != nil {
			return err
		}
		if filepath.IsAbs(link) {
			return fmt.Errorf("归档包含非法链接：%s", name)
		}
		pending = append(strings.Split(filepath.ToSlash(link), "/"), pending...)
	}
	return nil
}

// archiveContextReader keeps large entry copies responsive to cancellation.
type archiveContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r archiveContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

func extractZip(ctx context.Context, archive, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := safeJoin(".", f.Name)
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode.IsDir() {
			if err := root.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode.Perm()&0o111 != 0 {
			perm = 0o755
		}
		out, err := root.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, archiveContextReader{ctx: ctx, r: rc}); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

func extractTarGz(ctx context.Context, archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(archiveContextReader{ctx: ctx, r: f})
	if err != nil {
		return err
	}
	defer gz.Close()
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(archiveContextReader{ctx: ctx, r: gz})
	var symlinks []string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// Later entries can change the meaning of an earlier link's target.
			for _, name := range symlinks {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := safeArchiveLink(ctx, root, name); err != nil {
					_ = root.Remove(name)
					return err
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(".", hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Link names are relative to the link's parent, not the archive root.
			// Root.Symlink confines the link location but does not validate its value.
			link := filepath.FromSlash(hdr.Linkname)
			if filepath.IsAbs(link) {
				return fmt.Errorf("归档包含非法链接：%s", hdr.Linkname)
			}
			if err := safeArchiveLink(ctx, root, filepath.Dir(target)+string(filepath.Separator)+link); err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := root.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := root.Symlink(link, target); err != nil {
				return err
			}
			symlinks = append(symlinks, target)
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			perm := os.FileMode(hdr.Mode).Perm()
			if perm == 0 {
				perm = 0o644
			}
			out, err := root.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, archiveContextReader{ctx: ctx, r: tr}); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

// Flatten moves the contents of a single top-level directory up into dst,
// which is how most SDK archives are packed (node-v20.11.1-linux-x64/…).
func Flatten(dst string) error {
	entries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	// Ignore files that are not the archive's own root.
	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	if len(entries) != 1 || len(dirs) != 1 {
		return nil
	}
	root := filepath.Join(dst, dirs[0].Name())
	inner, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range inner {
		from := filepath.Join(root, e.Name())
		to := filepath.Join(dst, e.Name())
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return os.Remove(root)
}

// PlatformSlug names this machine the way release feeds do: node's
// "linux-x64", python-build-standalone's triples, and so on.
func PlatformSlug() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// NormalizeArch maps Go's architecture names to the ones feeds use.
func NormalizeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	case "arm64":
		return "arm64"
	default:
		return goarch
	}
}

// Triple is the Rust-style target triple of this machine.
func Triple() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "x86_64-pc-windows-msvc"
	case "windows/arm64":
		return "aarch64-pc-windows-msvc"
	case "darwin/amd64":
		return "x86_64-apple-darwin"
	case "darwin/arm64":
		return "aarch64-apple-darwin"
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu"
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu"
	case "linux/386":
		return "i686-unknown-linux-gnu"
	}
	return runtime.GOARCH + "-unknown-" + runtime.GOOS
}
