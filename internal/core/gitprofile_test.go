package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizeRemote pins the spellings that must collapse to one key. The key
// is what identities are bound to, so two spellings of one address failing to
// match would mean the wrong person's name on a commit.
func TestNormalizeRemote(t *testing.T) {
	same := []string{
		"git@github.com:acme/app.git",
		"git@github.com:acme/app",
		"https://github.com/acme/app.git",
		"https://github.com/acme/app",
		"https://github.com/acme/app/",
		"ssh://git@github.com/acme/app.git",
		"ssh://git@github.com:2222/acme/app.git",
		"https://user@github.com/acme/app",
		"https://token:x-oauth-basic@github.com/acme/app.git",
		"  git@github.com:acme/app.git  ",
		"https://GitHub.com/acme/app.git",
	}
	for _, url := range same {
		got, ok := NormalizeRemote(url)
		if !ok {
			t.Errorf("NormalizeRemote(%q) reported no address", url)
			continue
		}
		if got != "github.com/acme/app" {
			t.Errorf("NormalizeRemote(%q) = %q, want github.com/acme/app", url, got)
		}
	}

	// Each of these is a different address and must stay distinguishable.
	different := map[string]string{
		"git@gitlab.com:acme/app.git":  "gitlab.com/acme/app",
		"git@github.com:other/app.git": "github.com/other/app",
		// Ports are not part of the address a person chooses by.
		"ssh://git@gerrit.example.com:29418/team/app": "gerrit.example.com/team/app",
		"https://bitbucket.org/acme":                  "bitbucket.org/acme",
	}
	for url, want := range different {
		got, ok := NormalizeRemote(url)
		if !ok || got != want {
			t.Errorf("NormalizeRemote(%q) = %q, %v; want %q", url, got, ok, want)
		}
	}

	// Things that name no host cannot be matched by address.
	for _, url := range []string{
		"", "   ", "/home/dev/app", "./app", "../app", `C:\code\app`, "file:///home/dev/app", "myproject/src",
	} {
		if got, ok := NormalizeRemote(url); ok {
			t.Errorf("NormalizeRemote(%q) = %q, want no address", url, got)
		}
	}
}

// TestRemoteKeyLevels: one binding for an owner should also answer for every
// repository under it, so the levels go from most to least specific.
func TestRemoteKeyLevels(t *testing.T) {
	got := RemoteKeyLevels("git@github.com:acme/team/app.git")
	want := []string{"github.com/acme/team/app", "github.com/acme/team", "github.com/acme", "github.com"}
	if len(got) != len(want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("levels = %v, want %v", got, want)
		}
	}
}

// TestMatchGitProfile: the most specific binding wins, and a rule for a host does
// not steal a repository that has its own binding.
func TestMatchGitProfile(t *testing.T) {
	profiles := []GitProfile{
		{Key: "github.com", Name: "Host", Email: "host@x.dev"},
		{Key: "github.com/acme", Name: "Acme", Email: "acme@x.dev"},
		{Key: "github.com/acme/secret", Name: "Secret", Email: "secret@x.dev"},
		{Key: "gitlab.com", Name: "GitLab", Email: "gl@x.dev"},
	}

	cases := map[string]string{
		"git@github.com:acme/secret/app.git": "Secret",
		"git@github.com:acme/app.git":        "Acme",
		"git@github.com:acme":                "Acme",
		"git@github.com:someone/thing.git":   "Host",
		"https://gitlab.com/acme/app.git":    "GitLab",
	}
	for url, want := range cases {
		got := MatchGitProfile(url, profiles)
		if got == nil {
			t.Errorf("MatchGitProfile(%q) = nil, want %s", url, want)
			continue
		}
		if got.Name != want {
			t.Errorf("MatchGitProfile(%q) = %s, want %s", url, got.Name, want)
		}
	}
	if got := MatchGitProfile("git@bitbucket.org:a/b.git", profiles); got != nil {
		t.Errorf("unbound address matched %q", got.Key)
	}
	if got := MatchGitProfile("/home/dev/app", profiles); got != nil {
		t.Errorf("a local path matched %q", got.Key)
	}
}

// TestIncludePatternsCoverEverySpelling guards the set of patterns generated for
// one key. A missing pattern means git silently keeps the wrong identity for a
// clone done the other way.
func TestIncludePatternsCoverEverySpelling(t *testing.T) {
	pats := IncludePatterns("github.com/acme")
	joined := ""
	for _, p := range pats {
		joined += p + "\n"
	}
	for _, want := range []string{
		"git@github.com:acme/**",
		"https://github.com/acme/**",
		"http://github.com/acme/**",
		"https://*@github.com/acme/**",
		"ssh://*github.com*/acme/**",
		// The bare .git ending: a repository key needs it because the clone URL
		// ends in .git and `<path>/**` requires a slash after the path.
		"git@github.com:acme.git",
		"https://github.com/acme.git",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("patterns missing %q, got:\n%s", want, joined)
		}
	}

	// A host-only key covers every repository beneath it, using * and ** to
	// stand in for the parts that vary.
	hostPats := IncludePatterns("github.com")
	hostJoined := ""
	for _, p := range hostPats {
		hostJoined += p + "\n"
	}
	for _, want := range []string{"git@github.com:*", "https://github.com/**", "ssh://*github.com*/**"} {
		if !strings.Contains(hostJoined, want) {
			t.Errorf("host patterns missing %q, got:\n%s", want, hostJoined)
		}
	}
}

// TestProfileSlugIsFileSafe keeps keys with characters a file name cannot hold
// from producing a path that fails to write.
func TestProfileSlugIsFileSafe(t *testing.T) {
	cases := map[string]string{
		"github.com/acme":         "github.com__acme",
		"github.com/acme/team":    "github.com__acme__team",
		"gerrit.example.com/team": "gerrit.example.com__team",
	}
	for key, want := range cases {
		if got := ProfileSlug(key); got != want {
			t.Errorf("ProfileSlug(%q) = %q, want %q", key, got, want)
		}
	}
	for _, bad := range []string{":", "*", "?", `\`, "|", "<", ">"} {
		slug := ProfileSlug("host.com/a" + bad + "b")
		for _, ch := range bad {
			if strings.ContainsRune(slug, ch) {
				t.Errorf("slug %q still contains %q", slug, ch)
			}
		}
	}
}

// TestStripGitRules keeps the rest of the global config intact, which is the
// whole point of fencing the block.
func TestStripGitRules(t *testing.T) {
	original := `[user]
	name = Someone
	email = someone@example.dev

[alias]
	st = status
`
	withBlock := `[user]
	name = Someone
	email = someone@example.dev

` + gitRulesBegin + `
[includeIf "hasconfig:remote.*.url:git@github.com:acme/**"]
	path = /x/y.gitconfig
` + gitRulesEnd + `

[alias]
	st = status
`
	got := stripGitRules(withBlock)
	if got != original {
		t.Errorf("stripGitRules left:\n%q\nwant:\n%q", got, original)
	}
	// A user's own includeIf, outside the fence, must survive untouched.
	userOwn := `[user]
	name = Someone

[includeIf "hasconfig:remote.*.url:git@github.com:acme/**"]
	path = /x/mine.gitconfig
`
	if got := stripGitRules(userOwn); got != userOwn {
		t.Errorf("a hand-written includeIf was altered:\n%q", got)
	}
	// No block means no change.
	if again := stripGitRules(original); again != original {
		t.Error("stripGitRules changed a file with no block")
	}
	// A truncated block is dropped rather than half kept.
	truncated := "[user]\n\tname = X\n" + gitRulesBegin + "\n[includeIf \"x\"]\n\tpath = /y\n"
	if got := stripGitRules(truncated); got != "[user]\n\tname = X\n" {
		t.Errorf("stripGitRules on a truncated block = %q", got)
	}
}

// TestGitRulesTextStructure: every valid profile gets its patterns and the block
// is fenced.
func TestGitRulesTextStructure(t *testing.T) {
	text := GitRulesText([]GitProfile{
		{Key: "github.com/acme", Name: "Acme", Email: "a@x.dev"},
		{Key: "", Name: "Broken", Email: "b@x.dev"},       // no key: skipped
		{Key: "gitlab.com/team", Name: "Team", Email: ""}, // no email: skipped
	})
	if !strings.Contains(text, gitRulesBegin) || !strings.Contains(text, gitRulesEnd) {
		t.Fatal("block is not fenced")
	}
	if !strings.Contains(text, "Acme") && !strings.Contains(text, ProfileSlug("github.com/acme")) {
		t.Error("the valid profile is missing")
	}
	if strings.Contains(text, "Broken") || strings.Contains(text, "Team") {
		t.Error("invalid profiles must not be written")
	}
}

// TestProfileFileUnderConfigDir keeps the identity files inside EnvMgr's own
// directory, not scattered next to the global config.
func TestProfileFileUnderConfigDir(t *testing.T) {
	file := ProfileFile("github.com/acme")
	dir := GitConfigDir()
	rel, err := filepath.Rel(dir, file)
	if err != nil || rel == ".." || len(rel) > 1 && rel[:2] == ".." {
		t.Fatalf("ProfileFile(%q) = %q, not under %q", "github.com/acme", file, dir)
	}
	if filepath.Ext(file) != ".gitconfig" {
		t.Errorf("file %q should end in .gitconfig", file)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Logf("config dir does not exist yet (fine before first save): %v", err)
	}
}

// TestProfileDisplay: a name is what a person recognises, so it wins when it
// exists; otherwise the domain is shown instead of the whole address, which is
// what fits on a project tile.
func TestProfileDisplay(t *testing.T) {
	cases := []struct {
		label, key, want string
	}{
		{"公司账号", "github.com/acme/app", "公司账号"},
		{"  ", "github.com/acme/app", "github.com"},
		{"", "github.com/acme/app", "github.com"},
		{"", "gitlab.com", "gitlab.com"},
		{"", "", ""},
	}
	for _, c := range cases {
		p := GitProfile{Key: c.key, Label: c.label}
		if got := p.Display(); got != c.want {
			t.Errorf("GitProfile{Key:%q, Label:%q}.Display() = %q, want %q",
				c.key, c.label, got, c.want)
		}
	}
	// Domain is just the host part, and never the path.
	for key, want := range map[string]string{
		"github.com/acme/app":         "github.com",
		"gerrit.example.com/team/app": "gerrit.example.com",
		"localhost/x":                 "localhost",
		"":                            "",
	} {
		if got := Domain(key); got != want {
			t.Errorf("Domain(%q) = %q, want %q", key, got, want)
		}
	}
}
