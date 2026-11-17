package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GitProfile binds a name and email to a remote address. The address is the key
// because that is what actually changes between identities: the same person
// commits as one identity to a company's GitHub org and as another to their own
// repositories, and the remote URL is the only thing that reliably tells those
// apart.
//
// The key is normalized: scheme, credentials, port, and the .git suffix are
// dropped, so `git@github.com:acme/app.git` and
// `https://github.com/acme/app` are the same key. It may name a host, an
// owner, or one repository, and the most specific match wins.
type GitProfile struct {
	// Key is the normalized remote address, e.g. "github.com/acme".
	Key string
	// Label is the optional name the user gave this binding, such as "公司账号".
	// A name is what a person recognises at a glance, so it is shown instead of
	// the address whenever it exists.
	Label string
	Name  string
	Email string
	Note  string
	Added time.Time
}

// Valid reports whether the profile can be installed as a rule.
func (p GitProfile) Valid() bool {
	return p.Key != "" && p.Name != "" && p.Email != ""
}

// Display is what to show for this binding: the name if one was given, and
// otherwise the domain the address belongs to.
func (p GitProfile) Display() string {
	if label := strings.TrimSpace(p.Label); label != "" {
		return label
	}
	return Domain(p.Key)
}

// Domain is the host part of a key, which is the shortest useful way to name an
// address that has no name of its own.
func Domain(key string) string {
	host, _, _ := strings.Cut(key, "/")
	return host
}

// ProfileScope is the path part of a key: the owner or repository below the host.
// It is what distinguishes two bindings on the same server, so it is shown next to
// a binding that already has a name. A host-only key has no scope.
func ProfileScope(key string) string {
	_, path, ok := strings.Cut(key, "/")
	if !ok {
		return ""
	}
	return path
}

// NormalizeRemote turns a remote URL into the key shared by every spelling of
// the same address. It reports false for URLs that name no host, such as a
// local path, because those cannot be matched by address.
func NormalizeRemote(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	// A trailing .git and any trailing slash address the same repository.
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	if s == "" {
		return "", false
	}

	var host, path string
	switch {
	case strings.Contains(s, "://"):
		// scheme://[user[:pass]@]host[:port]/path
		rest := s[strings.Index(s, "://")+3:]
		host, path = cutOnce(rest, "/")
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		host = stripPort(host)
	case isScpLike(s):
		// [user@]host:path
		at := strings.LastIndex(s, "@")
		host, path = cutOnce(s[at+1:], ":")
	default:
		if looksLikeLocalPath(s) {
			return "", false
		}
		host, path = cutOnce(s, "/")
	}

	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.Trim(path, "/")
	if host == "" || looksLikeLocalPath(host) {
		return "", false
	}
	// A local directory has no host-like first component, so a dot (or
	// localhost) is required: "myproject/src" is a path, not an address.
	if !strings.Contains(host, ".") && host != "localhost" {
		return "", false
	}
	if path == "" {
		return host, true
	}
	return host + "/" + path, true
}

// cutOnce splits at the first separator.
func cutOnce(s, sep string) (before, after string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return s, ""
}

// isScpLike reports the `git@host:path` spelling, which has no scheme and uses a
// colon before any slash.
func isScpLike(s string) bool {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return false
	}
	colon := strings.Index(s[at:], ":")
	slash := strings.Index(s[at:], "/")
	return colon >= 0 && (slash < 0 || colon < slash)
}

// looksLikeLocalPath reports a filesystem path rather than a remote address.
func looksLikeLocalPath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return true
	}
	if strings.Contains(s, `\`) {
		return true
	}
	// A Windows drive letter, "C:" or "C:/".
	return len(s) >= 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'))
}

// stripPort removes a numeric port. The same server on 22 and 2222 is the same
// place to a person choosing an identity.
func stripPort(host string) string {
	if isIPv6Bracket(host) {
		return host
	}
	i := strings.LastIndex(host, ":")
	if i < 0 {
		return host
	}
	if _, err := strconv.Atoi(host[i+1:]); err != nil {
		return host
	}
	return host[:i]
}

func isIPv6Bracket(host string) bool { return strings.HasPrefix(host, "[") }

// RemoteKeyLevels lists the keys a URL could match, most specific first. A rule
// for github.com/acme applies to github.com/acme/app as well, which is how one
// binding can cover a whole organisation.
func RemoteKeyLevels(raw string) []string {
	key, ok := NormalizeRemote(raw)
	if !ok {
		return nil
	}
	parts := strings.Split(key, "/")
	levels := make([]string, 0, len(parts))
	for i := len(parts); i >= 1; i-- {
		levels = append(levels, strings.Join(parts[:i], "/"))
	}
	return levels
}

// MatchGitProfile picks the profile for a remote URL, preferring the most
// specific key. An exact repository binding beats an owner binding, which beats
// a whole-host binding.
func MatchGitProfile(remote string, profiles []GitProfile) *GitProfile {
	for _, level := range RemoteKeyLevels(remote) {
		for i := range profiles {
			if strings.EqualFold(profiles[i].Key, level) {
				return &profiles[i]
			}
		}
	}
	return nil
}

// IncludePatterns returns the `includeIf` patterns that make git apply a profile
// to every repository whose remote matches the key.
//
// Git matches these with wildmatch, and each URL spelling is matched literally: a
// rule written for `git@host:path` does not fire for `https://host/path`. All the
// forms below are needed so the binding holds no matter how somebody cloned.
//
// A repository's URL may or may not end in `.git`, and the key has that suffix
// stripped, so both endings are emitted. Without the bare `.git` form a binding
// for one repository would never match: `<path>/**` requires a slash after the
// path, and `.git` is not a slash.
func IncludePatterns(key string) []string {
	host, path, ok := strings.Cut(key, "/")
	if !ok {
		host = key
		path = ""
	}
	if path == "" {
		// A whole host: every repository on it. The trailing `*` covers
		// `owner/repo.git` without needing a separate pattern.
		return []string{
			"git@" + host + ":*",
			"https://" + host + "/**",
			"http://" + host + "/**",
			"https://*@" + host + "/**",
			"ssh://*" + host + "*/**",
		}
	}
	// `<path>/**` covers everything below the key (an organisation, an owner);
	// `<path>.git` and the bare `<path>` cover the key itself, because a clone URL
	// is written either with or without that suffix. Both endings are needed: the
	// suffix is stripped from the key, and `<path>/**` requires a slash after it.
	return []string{
		"git@" + host + ":" + path + "/**",
		"git@" + host + ":" + path + ".git",
		"git@" + host + ":" + path,
		"https://" + host + "/" + path + "/**",
		"https://" + host + "/" + path + ".git",
		"https://" + host + "/" + path,
		"http://" + host + "/" + path + "/**",
		"http://" + host + "/" + path + ".git",
		"http://" + host + "/" + path,
		"https://*@" + host + "/" + path + "/**",
		"https://*@" + host + "/" + path + ".git",
		"https://*@" + host + "/" + path,
		"ssh://*" + host + "*/" + path + "/**",
		"ssh://*" + host + "*/" + path + ".git",
		"ssh://*" + host + "*/" + path,
	}
}

// GitConfigDir is where EnvMgr keeps the per-profile identity files it includes
// from the global git config.
func GitConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".envmgr", "git")
}

// ProfileFile is the identity file for one profile key.
func ProfileFile(key string) string {
	return filepath.Join(GitConfigDir(), ProfileSlug(key)+".gitconfig")
}

// ProfileSlug turns a key into a file name. The mapping has to be reversible
// enough to recognise a file, so "github.com/acme" becomes "github.com__acme".
func ProfileSlug(key string) string {
	slug := strings.ReplaceAll(key, "/", "__")
	slug = strings.Map(func(r rune) rune {
		switch r {
		case ':', '*', '?', '"', '<', '>', '|', '\\':
			return '_'
		}
		return r
	}, slug)
	return slug
}

// gitRulesBegin and gitRulesEnd fence the block EnvMgr owns in the global git
// config, the same way the shell hook fences its block in a shell profile.
const (
	gitRulesBegin = "# >>> envmgr git identities >>>"
	gitRulesEnd   = "# <<< envmgr git identities <<<"
)

// GitRulesInstalled reports whether the global config currently carries an EnvMgr
// block.
func GitRulesInstalled() bool {
	text, err := os.ReadFile(globalGitConfig())
	if err != nil {
		return false
	}
	return strings.Contains(string(text), gitRulesBegin)
}

// GitRulesText is the block that would be written for these profiles.
func GitRulesText(profiles []GitProfile) string {
	var b strings.Builder
	b.WriteString(gitRulesBegin + "\n")
	b.WriteString("# 由 EnvMgr 生成：按远端地址选择提交身份。改动会在下次保存时被覆盖。\n")
	for _, p := range profiles {
		if !p.Valid() {
			continue
		}
		file := filepath.ToSlash(ProfileFile(p.Key))
		for _, pattern := range IncludePatterns(p.Key) {
			fmt.Fprintf(&b, "[includeIf \"hasconfig:remote.*.url:%s\"]\n\tpath = %s\n", pattern, file)
		}
	}
	b.WriteString(gitRulesEnd + "\n")
	return b.String()
}

// InstallGitRules writes the identity files and the include block into the global
// git config.
//
// This does write the global config, which is deliberate and narrow: it is the
// only mechanism git offers for choosing an identity by remote address, it
// cannot be expressed per repository, and the block is marked, rebuilt from
// scratch on every save, and the original file is backed up once. The plain
// global identity is never touched.
func InstallGitRules(profiles []GitProfile) error {
	dir := GitConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", dir, err)
	}
	// One identity file per key.
	for _, p := range profiles {
		if !p.Valid() {
			continue
		}
		body := "# 由 EnvMgr 生成：远端地址 " + p.Key + "\n[user]\n\tname = " + p.Name + "\n\temail = " + p.Email + "\n"
		if err := os.WriteFile(ProfileFile(p.Key), []byte(body), 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", ProfileFile(p.Key), err)
		}
	}
	// The block in the global config.
	file := globalGitConfig()
	original, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	backup := file + ".envmgr.bak"
	if _, statErr := os.Stat(backup); os.IsNotExist(statErr) && len(original) > 0 {
		if err := os.WriteFile(backup, original, 0o644); err != nil {
			return fmt.Errorf("备份 %s 失败: %w", file, err)
		}
	}
	cleaned := stripGitRules(string(original))
	next := strings.TrimRight(cleaned, "\r\n")
	if next != "" {
		next += "\n\n"
	}
	next += GitRulesText(profiles)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(next), 0o644)
}

// UninstallGitRules removes the EnvMgr block from the global git config. The
// identity files are left alone: they are inert without the block, and deleting
// them would lose what the user typed.
func UninstallGitRules() (bool, error) {
	file := globalGitConfig()
	original, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cleaned := stripGitRules(string(original))
	if cleaned == string(original) {
		return false, nil
	}
	return true, os.WriteFile(file, []byte(strings.TrimRight(cleaned, "\r\n")), 0o644)
}

// stripGitRules removes the fenced block, leaving the rest of the file alone.
func stripGitRules(text string) string {
	start := strings.Index(text, gitRulesBegin)
	if start < 0 {
		return text
	}
	end := strings.Index(text[start:], gitRulesEnd)
	if end < 0 {
		// A truncated block: drop from the marker to the end rather than leave
		// half a block behind.
		return text[:start]
	}
	end += start + len(gitRulesEnd)
	// Also eat the newline that followed the closing marker.
	for end < len(text) && (text[end] == '\r' || text[end] == '\n') {
		end++
	}
	return text[:start] + text[end:]
}

// globalGitConfig finds the global config file git itself would use.
func globalGitConfig() string {
	if p := os.Getenv("GIT_CONFIG_GLOBAL"); p != "" {
		return filepath.FromSlash(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gitconfig"
	}
	// git reads ~/.gitconfig first, then $XDG_CONFIG_HOME/git/config.
	dot := filepath.Join(home, ".gitconfig")
	if _, err := os.Stat(dot); err == nil {
		return dot
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	alt := filepath.Join(xdg, "git", "config")
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return dot
}

// GlobalGitConfig is the file the rules are written to, for display.
func GlobalGitConfig() string { return globalGitConfig() }

// GitVersion returns the parsed major and minor version of the git program.
func GitVersion() (major, minor int, ok bool) {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(out))
	for _, f := range fields {
		if f == "" || f[0] < '0' || f[0] > '9' {
			continue
		}
		parts := strings.SplitN(f, ".", 3)
		if len(parts) < 2 {
			continue
		}
		maj, err1 := strconv.Atoi(parts[0])
		min, err2 := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool {
			return r < '0' || r > '9'
		}))
		if err1 == nil && err2 == nil {
			return maj, min, true
		}
	}
	return 0, 0, false
}

// IncludeIfByRemoteSupported reports whether this git understands
// `hasconfig:remote.*.url`, which arrived in 2.36.
func IncludeIfByRemoteSupported() bool {
	major, minor, ok := GitVersion()
	if !ok {
		return false
	}
	if major != 2 {
		return major > 2
	}
	return minor >= 36
}
