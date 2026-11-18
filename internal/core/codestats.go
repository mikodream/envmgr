package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// CodeStats describes source files, not documentation or configuration. CodeLines
// means nonblank text lines (including comments); the GUI should label it 非空行.
// BlankLines counts whitespace-only lines. A trailing newline adds no extra line.
// Bytes includes only accepted files. Skipped counts encountered excluded files
// or pruned directories, not the unseen descendants of those directories.
// Truncated means a work budget or cancellation stopped the scan. Err retains
// the first error; all totals still describe the successfully scanned files.
type CodeStats struct {
	Files, CodeLines, BlankLines int
	Bytes                        int64
	Languages                    []LanguageStats
	Skipped                      int
	Truncated                    bool
	Err                          string
}

// LanguageStats uses the same line and byte definitions as CodeStats.
type LanguageStats struct {
	Language                     string
	Files, CodeLines, BlankLines int
	Bytes                        int64
}

const (
	codeStatsMaxFileBytes = 2 << 20
	codeStatsMaxFiles     = 20000
	codeStatsMaxEntries   = 100000
	codeStatsMaxReadBytes = 128 << 20
	codeStatsMaxGitBytes  = 8 << 20
)

type codeStatsLimits struct {
	fileBytes, readBytes int64
	files, entries       int
}

// LoadCodeStats is a synchronous, cancellation-aware loader for a caller-owned
// background job/cache. It creates no goroutines that outlive the call. Git work
// trees use one scoped ls-files enumeration, respecting Git's ignore rules for
// untracked files. Tracked files remain included even if subsequently ignored.
// Without Git or outside a work tree, WalkDir applies the directory exclusions
// below, without attempting to approximate .gitignore. Both paths exclude these
// directories even when tracked, and skip symlinks and nonregular files.
func LoadCodeStats(ctx context.Context, dir string) CodeStats {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return loadCodeStats(ctx, dir, codeStatsLimits{
		fileBytes: codeStatsMaxFileBytes, readBytes: codeStatsMaxReadBytes,
		files: codeStatsMaxFiles, entries: codeStatsMaxEntries,
	})
}

func loadCodeStats(ctx context.Context, dir string, limits codeStatsLimits) CodeStats {
	s := codeStatsScan{ctx: ctx, limits: limits, languages: make(map[string]*LanguageStats)}
	if s.stopped() {
		return s.result()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		s.fail(err)
		return s.result()
	}
	info, err := os.Lstat(abs)
	if err != nil {
		s.fail(err)
		return s.result()
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		s.fail(fmt.Errorf("code stats project is not a regular directory: %s", abs))
		return s.result()
	}
	// Resolve parent links once; all subsequent file reads are confined by Root,
	// including if an entry is replaced with an escaping link during a scan.
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		s.fail(err)
		return s.result()
	}
	s.root, err = os.OpenRoot(abs)
	if err != nil {
		s.fail(err)
		return s.result()
	}
	defer s.root.Close()

	probe, stderr, _, gitErr := codeStatsGit(ctx, abs, 4096, "rev-parse", "--is-inside-work-tree")
	if s.stopped() {
		return s.result()
	}
	var missing *exec.Error
	fallback := errors.As(gitErr, &missing) || strings.Contains(string(stderr), "not a git repository")
	if gitErr == nil && strings.TrimSpace(string(probe)) == "true" {
		paths, stderr, capped, err := codeStatsGit(ctx, abs, codeStatsMaxGitBytes,
			"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate", "--", ".")
		if capped {
			s.stats.Truncated = true
		} else if err != nil && ctx.Err() == nil {
			s.fail(fmt.Errorf("git ls-files: %w: %s", err, strings.TrimSpace(string(stderr))))
		}
		seen := make(map[string]bool)
		for len(paths) > 0 && !s.stopped() {
			n := bytes.IndexByte(paths, 0)
			if n < 0 { // A capped listing may end halfway through a path.
				break
			}
			rel := string(paths[:n])
			paths = paths[n+1:]
			if seen[rel] {
				continue
			}
			if !s.visit() {
				break
			}
			seen[rel] = true
			s.file(rel)
		}
	} else if gitErr == nil || fallback {
		err := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, err error) error {
			if s.stopped() {
				return fs.SkipAll
			}
			if err != nil {
				s.stats.Skipped++
				s.fail(err)
				return nil
			}
			if path == abs {
				return nil
			}
			if !s.visit() {
				return fs.SkipAll
			}
			if entry.IsDir() {
				if codeStatsExcludedDir(entry.Name()) {
					s.stats.Skipped++
					return fs.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(abs, path)
			if err != nil {
				s.fail(err)
				return nil
			}
			s.file(rel)
			return nil
		})
		if err != nil {
			s.fail(err)
		}
	} else {
		s.fail(fmt.Errorf("git work tree: %w: %s", gitErr, strings.TrimSpace(string(stderr))))
	}
	s.stopped() // Preserve cancellation even when it coincides with the last file.
	return s.result()
}

type codeStatsScan struct {
	ctx       context.Context
	root      *os.Root
	limits    codeStatsLimits
	stats     CodeStats
	languages map[string]*LanguageStats
	entries   int
	readBytes int64
	halted    bool
}

func (s *codeStatsScan) fail(err error) {
	if s.stats.Err == "" {
		s.stats.Err = err.Error()
	}
}

func (s *codeStatsScan) stopped() bool {
	if err := s.ctx.Err(); err != nil {
		s.stats.Truncated = true
		s.fail(err)
		s.halted = true
	}
	return s.halted
}

func (s *codeStatsScan) visit() bool {
	if s.stopped() {
		return false
	}
	if s.entries >= s.limits.entries || s.stats.Files >= s.limits.files {
		s.stats.Truncated = true
		s.halted = true
		return false
	}
	s.entries++
	return true
}

func (s *codeStatsScan) file(rel string) {
	// Git paths are untrusted too: reject absolute paths, traversal and every
	// symlink component before opening. Root provides the race-safe boundary.
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) {
		s.stats.Skipped++
		return
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for i, part := range parts {
		if codeStatsExcludedDir(part) && i < len(parts)-1 {
			s.stats.Skipped++
			return
		}
		info, err := s.root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			s.stats.Skipped++
			s.fail(err)
			return
		}
		if info.Mode()&os.ModeSymlink != 0 || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			s.stats.Skipped++
			return
		}
	}
	language := codeStatsLanguage(rel)
	if language == "" {
		s.stats.Skipped++
		return
	}
	file, err := s.root.Open(rel)
	if err != nil {
		s.stats.Skipped++
		s.fail(err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.stats.Skipped++
		s.fail(err)
		return
	}
	if !info.Mode().IsRegular() || info.Size() > s.limits.fileBytes {
		s.stats.Skipped++
		return
	}
	remaining := s.limits.readBytes - s.readBytes
	if info.Size() > remaining {
		s.stats.Truncated = true
		s.halted = true
		return
	}
	limit := min(s.limits.fileBytes, remaining)
	var data bytes.Buffer
	buf := make([]byte, 32*1024)
	for !s.stopped() {
		n, readErr := file.Read(buf[:min(int64(len(buf)), limit-int64(data.Len())+1)])
		s.readBytes += int64(n)
		data.Write(buf[:n])
		if int64(data.Len()) > limit {
			if limit < s.limits.fileBytes {
				s.stats.Truncated = true
				s.halted = true
			}
			s.stats.Skipped++
			return
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			s.stats.Skipped++
			s.fail(readErr)
			return
		}
	}
	if s.stopped() {
		return
	}
	text := data.Bytes()
	if bytes.IndexByte(text, 0) >= 0 || !utf8.Valid(text) {
		s.stats.Skipped++
		return
	}
	code, blank := codeStatsLines(text)
	ls := s.languages[language]
	if ls == nil {
		ls = &LanguageStats{Language: language}
		s.languages[language] = ls
	}
	ls.Files++
	ls.CodeLines += code
	ls.BlankLines += blank
	ls.Bytes += int64(len(text))
	s.stats.Files++
	s.stats.CodeLines += code
	s.stats.BlankLines += blank
	s.stats.Bytes += int64(len(text))
}

func (s *codeStatsScan) result() CodeStats {
	for _, language := range s.languages {
		s.stats.Languages = append(s.stats.Languages, *language)
	}
	sort.Slice(s.stats.Languages, func(i, j int) bool {
		a, b := s.stats.Languages[i], s.stats.Languages[j]
		if a.CodeLines != b.CodeLines {
			return a.CodeLines > b.CodeLines
		}
		return a.Language < b.Language
	})
	return s.stats
}

func codeStatsLines(text []byte) (code, blank int) {
	for len(text) > 0 {
		line, rest, found := bytes.Cut(text, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			blank++
		} else {
			code++
		}
		if !found {
			break
		}
		text = rest
	}
	return
}

func codeStatsExcludedDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor", ".venv", "venv", "dist", "build", "target", "__pycache__", "coverage", ".next", ".nuxt", ".output", ".svelte-kit", "bin", "obj":
		return true
	}
	return false
}

// This explicit source allowlist intentionally omits JSON, YAML, Markdown,
// lockfiles and other documentation/configuration rather than calling them code.
func codeStatsLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "Go"
	case ".ts", ".tsx", ".mts", ".cts":
		return "TypeScript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "JavaScript"
	case ".vue":
		return "Vue"
	case ".svelte":
		return "Svelte"
	case ".py", ".pyw", ".pyi":
		return "Python"
	case ".java":
		return "Java"
	case ".rs":
		return "Rust"
	case ".c", ".h":
		return "C"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return "C++"
	case ".cs":
		return "C#"
	case ".html", ".htm":
		return "HTML"
	case ".css", ".scss", ".sass", ".less":
		return "CSS"
	case ".sh", ".bash", ".zsh", ".fish", ".ps1", ".psm1", ".bat", ".cmd":
		return "Shell"
	case ".sql":
		return "SQL"
	case ".rb", ".rake":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".swift":
		return "Swift"
	case ".kt", ".kts":
		return "Kotlin"
	case ".scala":
		return "Scala"
	case ".dart":
		return "Dart"
	case ".lua":
		return "Lua"
	case ".r":
		return "R"
	case ".ex", ".exs":
		return "Elixir"
	case ".erl", ".hrl":
		return "Erlang"
	case ".hs", ".lhs":
		return "Haskell"
	case ".clj", ".cljs", ".cljc":
		return "Clojure"
	case ".fs", ".fsx":
		return "F#"
	case ".m", ".mm":
		return "Objective-C"
	case ".pl", ".pm":
		return "Perl"
	case ".zig":
		return "Zig"
	case ".asm", ".s":
		return "Assembly"
	case ".proto":
		return "Protocol Buffers"
	case ".graphql", ".gql":
		return "GraphQL"
	}
	return ""
}

type codeStatsOutput struct {
	buffer bytes.Buffer
	limit  int
	capped bool
	cancel context.CancelFunc
}

func (b *codeStatsOutput) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		n, _ := b.buffer.Write(p[:remaining])
		b.capped = true
		b.cancel()
		return n, fmt.Errorf("code stats Git output budget exceeded")
	}
	return b.buffer.Write(p)
}

func codeStatsGit(ctx context.Context, dir string, limit int, args ...string) (stdout, stderr []byte, capped bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--literal-pathspecs", "-C", dir}, args...)...)
	// Ambient repository overrides must not redirect a project scan elsewhere.
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(name) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX", "GIT_OPTIONAL_LOCKS", "GIT_TERMINAL_PROMPT", "LC_ALL":
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out := &codeStatsOutput{limit: limit, cancel: cancel}
	diagnostic := &codeStatsOutput{limit: 4096, cancel: cancel}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	return out.buffer.Bytes(), diagnostic.buffer.Bytes(), out.capped, err
}
