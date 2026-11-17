package core

import (
	"fmt"
	"os/exec"
	"strings"
)

// Where a value came from. Git has no per-project identity of its own: a value
// is either written into the repository's config, supplied by a conditional
// include that matched this repository's remote, or inherited from the plain
// global config. The UI has to say which one answered, otherwise "the email is
// wrong" is impossible to diagnose.
type Source string

const (
	// SourceProject is a value written into this repository's own .git/config.
	SourceProject Source = "project"
	// SourceRule is a value supplied by a remote-matched include written by
	// EnvMgr: the identity was chosen because the remote address matched.
	SourceRule Source = "rule"
	// SourceGlobal is the plain global value, shared by every repository.
	SourceGlobal Source = "global"
	SourceNone   Source = "none"
)

// GitIdentity is the name and email Git will use for commits in a directory,
// with the origin of each value kept alongside it.
type GitIdentity struct {
	Name        string
	Email       string
	NameSource  Source
	EmailSource Source
	// NameOrigin and EmailOrigin are the config files that answered, straight
	// from `git config --show-origin`. They are what makes "where did this come
	// from" answerable instead of guessed.
	NameOrigin  string
	EmailOrigin string
	// The raw values, so the editor can show what would come back after
	// clearing the project setting.
	LocalName   string
	LocalEmail  string
	GlobalName  string
	GlobalEmail string
}

// Complete reports whether a commit would have both fields.
func (i GitIdentity) Complete() bool { return i.Name != "" && i.Email != "" }

// GitInfo is everything the project page shows about a directory's repository.
type GitInfo struct {
	// Available is false when the git program itself is missing.
	Available bool
	// Repo is true when the directory is inside a work tree.
	Repo   bool
	Root   string
	Branch string
	// Detached is a checkout with no branch. It is separate from Branch
	// because a freshly initialised repository also has no branch name from
	// `rev-parse`, yet it is not detached.
	Detached bool
	// Commits is false for a repository that has no commit yet, which is the
	// normal state right after `git init`.
	Commits bool
	Remote  string
	// Commit counts for the status line.
	Modified  int
	Staged    int
	Untracked int
	// Err is a literal message for the one case that needs explaining: git is
	// installed but the directory could not be read.
	Err      string
	Identity GitIdentity
}

// Clean reports a work tree with nothing to commit.
func (g GitInfo) Clean() bool { return g.Modified == 0 && g.Staged == 0 && g.Untracked == 0 }

// Changed counts every path that differs from HEAD.
func (g GitInfo) Changed() int { return g.Modified + g.Staged + g.Untracked }

// gitRunner runs one git command in a directory and returns stdout. It is the
// seam that keeps the parsing below testable without a real repository.
type gitRunner func(dir string, args ...string) (string, error)

// runGit is the real runner.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Keep git from opening a pager or waiting on a credential prompt: this is
	// called from the draw loop and must never block.
	cmd.Env = append(cmd.Environ(),
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	)
	var out strings.Builder
	cmd.Stdout = &out
	err := cmd.Run()
	// Only line endings are trimmed. `git status --porcelain` reports the index
	// state in the first column, so a leading space is data: trimming it turns
	// " M file" (modified) into "M file" (staged).
	return trimNewlines(out.String()), err
}

// trimNewlines drops trailing CR/LF without touching spaces at either end.
func trimNewlines(s string) string {
	return strings.TrimRight(s, "\r\n")
}

// LoadGit reads a directory's repository state using the real git program.
func LoadGit(dir string) GitInfo {
	return LoadGitIn(dir, gitEnv{})
}

// LoadGitIn reads a directory with the EnvMgr-specific locations supplied, so a
// value can be attributed to a remote-matched rule instead of the plain global
// config.
func LoadGitIn(dir string, env gitEnv) GitInfo {
	return loadGit(dir, runGit, exec.LookPath, env)
}

// gitEnv tells the reader where EnvMgr keeps the files it writes, so a value's
// origin can be attributed correctly.
type gitEnv struct {
	// rulesDir holds one identity file per remote-address profile.
	rulesDir string
	// globalPath is the main global config file, used to read the plain global
	// value even when a conditional include would otherwise answer.
	globalPath string
}

// loadGit is LoadGit with the program, the runner and the locations injected.
func loadGit(dir string, run gitRunner, lookPath func(string) (string, error), env gitEnv) GitInfo {
	if _, err := lookPath("git"); err != nil {
		return GitInfo{}
	}
	info := GitInfo{Available: true}

	inside, err := run(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		// Not a repository. Identity still matters, because that is what a
		// commit would use once the directory is initialised. Without a
		// repository no remote can match, so only the plain global applies.
		info.Identity = readIdentity(run, dir, env, func(string) string { return "" })
		return info
	}
	info.Repo = true
	info.Root = firstLine(mustRun(run, dir, "rev-parse", "--show-toplevel"))
	info.Branch, info.Detached = readBranch(run, dir)
	// `rev-parse HEAD` fails exactly when the repository has no commit, which
	// is the state right after `git init`.
	if _, err := run(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		info.Commits = true
	}
	info.Remote = firstLine(mustRun(run, dir, "config", "--get", "remote.origin.url"))
	// The porcelain output is parsed as-is: its first column is the index
	// state, and trimming would shift every line one character left.
	info.Staged, info.Modified, info.Untracked = countPorcelain(mustRun(run, dir, "status", "--porcelain"))

	local := func(key string) string {
		// --local only reads the repository config, so the two origins stay
		// distinguishable.
		v, err := run(dir, "config", "--local", "--get", key)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}
	info.Identity = readIdentity(run, dir, env, local)
	return info
}

// readIdentity resolves the effective name and email together with the file each
// one came from. `git config --show-origin` is what makes this exact rather than
// inferred: it names the file that answered, so a remote-matched include is
// distinguishable from the plain global value.
func readIdentity(run gitRunner, dir string, env gitEnv, local func(string) string) GitIdentity {
	name, nameOrigin := showOrigin(run, dir, "user.name")
	email, emailOrigin := showOrigin(run, dir, "user.email")

	id := GitIdentity{
		Name: name, Email: email,
		NameOrigin: nameOrigin, EmailOrigin: emailOrigin,
		LocalName:   local("user.name"),
		LocalEmail:  local("user.email"),
		GlobalName:  plainGlobal(run, dir, env, "user.name"),
		GlobalEmail: plainGlobal(run, dir, env, "user.email"),
	}
	id.NameSource = sourceFor(name, nameOrigin, dir, env)
	id.EmailSource = sourceFor(email, emailOrigin, dir, env)
	return id
}

// sourceFor names the kind of setting that produced a value.
func sourceFor(value, origin, dir string, env gitEnv) Source {
	if value == "" {
		return SourceNone
	}
	return classifyOrigin(origin, dir, env)
}

// showOrigin runs `git config --show-origin --get` and splits the file from the
// value. The output is "file:<path>\t<value>"; a bare value means git reported
// no origin.
func showOrigin(run gitRunner, dir, key string) (value, origin string) {
	out, err := run(dir, "config", "--show-origin", "--get", key)
	if err != nil {
		return "", ""
	}
	out = trimNewlines(out)
	if i := strings.IndexByte(out, '\t'); i >= 0 {
		origin, out = out[:i], out[i+1:]
	}
	value = strings.TrimSpace(out)
	origin = strings.TrimSpace(strings.TrimPrefix(origin, "file:"))
	if value == "" {
		return "", ""
	}
	return value, origin
}

// classifyOrigin decides which kind of setting answered, from the file git named.
func classifyOrigin(origin, dir string, env gitEnv) Source {
	if origin == "" {
		return SourceNone
	}
	origin = normalizeOrigin(origin, dir)
	// The repository's own config: git reports it relative to the work tree.
	if isRepoConfig(origin) {
		return SourceProject
	}
	if env.rulesDir != "" && underDir(origin, env.rulesDir) {
		return SourceRule
	}
	return SourceGlobal
}

// normalizeOrigin makes an origin absolute so it can be compared with a
// directory, without importing path logic beyond what is needed.
func normalizeOrigin(origin, dir string) string {
	origin = strings.ReplaceAll(origin, "\\", "/")
	if len(origin) > 1 && origin[1] == ':' || strings.HasPrefix(origin, "/") {
		return origin
	}
	// Relative to the work tree: ".git/config".
	return strings.ReplaceAll(dir, "\\", "/") + "/" + origin
}

// isRepoConfig reports whether a path is a repository's own config file.
func isRepoConfig(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, "/config") {
		return false
	}
	// ".git/config" for a normal repository, or ".git/worktrees/<name>/config"
	// for a linked worktree.
	return strings.Contains(p, "/.git/")
}

// underDir reports whether path sits inside dir.
func underDir(path, dir string) bool {
	path = strings.ReplaceAll(path, "\\", "/")
	dir = strings.ReplaceAll(dir, "\\", "/")
	dir = strings.TrimSuffix(dir, "/")
	return len(path) > len(dir) && strings.EqualFold(path[:len(dir)+1], dir+"/")
}

// plainGlobal reads one global setting from the main global config file only. It
// bypasses conditional includes on purpose: the editor needs the value that
// would come back if the remote rule went away, and `--global --get` would
// happily answer with a rule's value when the remote matches.
func plainGlobal(run gitRunner, dir string, env gitEnv, key string) string {
	if env.globalPath != "" {
		if v, err := run(dir, "config", "--file", env.globalPath, "--get", key); err == nil {
			return strings.TrimSpace(v)
		}
		return ""
	}
	v, err := run(dir, "config", "--global", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// firstLine is the trimmed first line of a single-value command's output.
func firstLine(out string) string {
	if i := strings.IndexAny(out, "\r\n"); i >= 0 {
		out = out[:i]
	}
	return strings.TrimSpace(out)
}

// readBranch finds the current branch name and whether HEAD is detached.
//
// `symbolic-ref` is asked first because it answers the branch even before the
// first commit: a freshly initialised repository has no commit, so
// `rev-parse --abbrev-ref HEAD` prints "HEAD" and cannot tell "not on a branch"
// apart from "branch exists but is unborn".
func readBranch(run gitRunner, dir string) (branch string, detached bool) {
	if name, err := run(dir, "symbolic-ref", "--short", "HEAD"); err == nil {
		if name = strings.TrimSpace(name); name != "" {
			return name, false
		}
	}
	// No symbolic HEAD: either a real detached checkout, or git could not say.
	if name, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if name = strings.TrimSpace(name); name != "" && name != "HEAD" {
			return name, false
		}
	}
	return "", true
}

// mustRun returns stdout and swallows the error: a status read that fails just
// means nothing is known about the work tree.
func mustRun(run gitRunner, dir string, args ...string) string {
	out, _ := run(dir, args...)
	return out
}

// countPorcelain counts the entries of `git status --porcelain`. The first
// column is the index, the second the work tree, and "??" means untracked, so
// one file can be staged and modified at the same time.
func countPorcelain(out string) (staged, modified, untracked int) {
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		x, y := line[0], line[1]
		if x == '?' && y == '?' {
			untracked++
			continue
		}
		if x != ' ' && x != '?' {
			staged++
		}
		if y != ' ' && y != '?' {
			modified++
		}
	}
	return staged, modified, untracked
}

// InitGitRepo turns a directory into a repository. The branch name is left to
// the user's init.defaultBranch, which is the setting git itself would use.
func InitGitRepo(dir string) (GitInfo, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return GitInfo{}, fmt.Errorf("没有找到 git，请先安装 Git")
	}
	if _, err := runGit(dir, "init"); err != nil {
		return GitInfo{}, fmt.Errorf("git init 失败: %w", err)
	}
	return LoadGit(dir), nil
}

// WriteGitIdentity writes the repository-level name and email. An empty value
// removes the repository setting rather than writing an empty string, which is
// how the caller asks for "use the global one again".
func WriteGitIdentity(dir, name, email string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("没有找到 git，请先安装 Git")
	}
	if err := setLocalValue(dir, "user.name", name); err != nil {
		return err
	}
	return setLocalValue(dir, "user.email", email)
}

func setLocalValue(dir, key, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		// Already absent is the desired state, so a failed unset is fine.
		_, _ = runGit(dir, "config", "--local", "--unset", key)
		return nil
	}
	if _, err := runGit(dir, "config", "--local", key, value); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", key, err)
	}
	return nil
}

// GitAvailable reports whether the git program can be found at all.
func GitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}
