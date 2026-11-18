package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGitRemoteEnumerationPreservesOriginFallback(t *testing.T) {
	f := &fakeGit{responses: map[string]string{
		"rev-parse --is-inside-work-tree": "true",
		"config --get remote.origin.url":  "git@github.com:legacy/app.git",
		"remote":                          "gitee\norigin",
		"remote get-url -- gitee":         "https://gitee.com/acme/app.git",
		"remote get-url --push -- gitee":  "git@gitee.com:publish/app.git",
	}}
	info := loadGit("/repo", f.run, okLook, gitEnv{})
	if len(info.Remotes) != 2 || info.Remotes[0].Name != "gitee" || info.Remotes[0].PushURL != "git@gitee.com:publish/app.git" {
		t.Fatalf("enumerated remotes = %+v", info.Remotes)
	}
	if info.Remote != "git@github.com:legacy/app.git" {
		t.Fatalf("origin fallback = %q", info.Remote)
	}
}

func profileTestService(t *testing.T) *Service {
	t.Helper()
	store, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	return &Service{store: store}
}

func TestGitMultipleRemotesRoundTrip(t *testing.T) {
	_, home := isolateGit(t)
	dir := makeRepo(t, home, "multi", "")
	s := profileTestService(t)
	urls := map[string]string{
		"github": "git@github.com:acme/app.git",
		"gitee":  "https://gitee.com/acme/app.git",
		"gitea":  "ssh://git@gitea.example.com:2222/acme/app.git",
	}
	for name, url := range urls {
		if err := s.SetGitRemote(dir, name, url); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	push := "git@gitee.com:publish/app.git"
	if _, err := runGit(dir, "remote", "set-url", "--push", "gitee", push); err != nil {
		t.Fatal(err)
	}
	updated := "https://gitee.com/other/app.git"
	if err := s.SetGitRemote(dir, "gitee", updated); err != nil {
		t.Fatal(err)
	}
	info := s.Git(dir)
	if len(info.Remotes) != 3 {
		t.Fatalf("remotes = %+v", info.Remotes)
	}
	if info.Remote != info.Remotes[0].URL {
		t.Errorf("without origin fallback = %q", info.Remote)
	}
	for _, r := range info.Remotes {
		want := urls[r.Name]
		if r.Name == "gitee" {
			want = updated
			if r.PushURL != push {
				t.Errorf("explicit push URL changed: %+v", r)
			}
		} else if r.PushURL != r.URL {
			t.Errorf("default push URL = %+v", r)
		}
		if r.URL != want {
			t.Errorf("remote = %+v, want URL %q", r, want)
		}
	}
	origin := "https://github.com/origin/app.git"
	if err := s.SetGitRemote(dir, "origin", origin); err != nil {
		t.Fatal(err)
	}
	if got := s.Git(dir).Remote; got != origin {
		t.Fatalf("origin fallback = %q", got)
	}
	if err := s.RemoveGitRemote(dir, "github"); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Git(dir).Remotes); got != 3 {
		t.Fatalf("after removal got %d remotes", got)
	}
	for _, name := range []string{"", "-bad", "bad name", "../bad", "bad..name"} {
		if err := s.SetGitRemote(dir, name, origin); err == nil {
			t.Errorf("accepted remote name %q", name)
		}
	}
	for _, url := range []string{"", "https://github.com/a\nb"} {
		if err := s.SetGitRemote(dir, "bad", url); err == nil {
			t.Errorf("accepted URL %q", url)
		}
	}
	if err := s.RemoveGitRemote(dir, "missing"); err == nil {
		t.Error("missing remote removal should report Git's error")
	}
	if err := s.SetGitRemote(home, "origin", origin); err == nil {
		t.Error("adding outside a repository succeeded")
	}
}

func TestGitMultipleIdentitiesSelectAndReload(t *testing.T) {
	global, home := isolateGit(t)
	before, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	s := profileTestService(t)
	a := GitProfile{Key: "git@github.com:acme/app.git", Name: "First", Email: "first@example.dev"}
	b := GitProfile{Key: "https://github.com/acme/app", Name: "Second", Email: "second@example.dev", Active: true}
	for _, p := range []GitProfile{a, b, {Key: "gitee.com/acme", Name: "Gitee", Email: "gitee@example.dev"}} {
		if err := s.SaveGitProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	profiles := s.GitProfiles()
	if len(profiles) != 3 {
		t.Fatalf("profiles = %+v", profiles)
	}
	var first, second GitProfile
	for _, p := range profiles {
		if p.Name == "First" {
			first = p
		}
		if p.Name == "Second" {
			second = p
		}
	}
	if first.ID == "" || second.ID == "" || first.ID == second.ID || !first.Active || second.Active {
		t.Fatalf("first/additional selection = %+v / %+v", first, second)
	}
	// A no-ID save with the same identity updates its metadata, preserving ID.
	a.Label = "updated"
	if err := s.SaveGitProfile(a); err != nil {
		t.Fatal(err)
	}
	if got := s.MatchGitProfileFor(a.Key); got == nil || got.Label != "updated" || got.ID != first.ID {
		t.Fatalf("same-pair upsert = %+v", got)
	}
	dir := makeRepo(t, home, "app", a.Key)
	if err := s.SelectGitProfile(dir, second.IdentityID()); err != nil {
		t.Fatal(err)
	}
	if got := s.Git(dir).Identity; got.Name != "Second" || got.NameSource != SourceProject {
		t.Fatalf("local identity = %+v", got)
	}
	if got := s.MatchGitProfileFor(a.Key); got == nil || got.ID != second.ID {
		t.Fatalf("active profile = %+v", got)
	}
	after, _ := os.ReadFile(global)
	if string(after) != string(before) || s.GitRulesInstalled() {
		t.Fatal("save/select installed unrequested rules")
	}
	// Reopening disk state preserves every identity, ID, and active selection.
	reopened := profileTestService(t)
	if len(reopened.GitProfiles()) != 3 {
		t.Fatal("identities lost during persistence")
	}
	if got := reopened.MatchGitProfileFor(a.Key); got == nil || got.ID != second.ID || !got.Active {
		t.Fatalf("reloaded active = %+v", got)
	}
	if err := reopened.InstallGitRules(); err != nil {
		t.Fatal(err)
	}
	other := makeRepo(t, home, "other", "https://github.com/acme/app.git")
	if got := effectiveName(t, other); got != "Second" {
		t.Fatalf("installed selection = %q", got)
	}
	if err := reopened.SelectGitProfile(dir, first.ID); err != nil {
		t.Fatal(err)
	}
	if got := effectiveName(t, dir); got != "First" {
		t.Fatalf("selected local identity = %q", got)
	}
	if got := effectiveName(t, other); got != "First" {
		t.Fatalf("refreshed global selection = %q", got)
	}
	// Editing by ID changes exactly that profile and retains its ID/selection.
	first.Name, first.Email = "Edited", "edited@example.dev"
	if err := reopened.SaveGitProfile(first); err != nil {
		t.Fatal(err)
	}
	if got := reopened.MatchGitProfileFor(a.Key); got == nil || got.ID != first.ID || got.Name != "Edited" {
		t.Fatalf("ID edit = %+v", got)
	}
	if len(reopened.GitProfiles()) != 3 {
		t.Fatal("ID edit added or removed an identity")
	}
	if got := effectiveName(t, dir); got != "First" {
		t.Fatalf("saving unexpectedly changed explicit repo identity: %q", got)
	}
	if err := reopened.SelectGitProfile(dir, "missing"); err == nil {
		t.Fatal("unknown identity selected")
	}
	if err := reopened.DeleteGitProfile(first.ID); err != nil {
		t.Fatal(err)
	}
	if got := reopened.MatchGitProfileFor(a.Key); got == nil || got.ID != second.ID || !got.Active {
		t.Fatalf("selection after delete = %+v", got)
	}
	if got := effectiveName(t, other); got != "Second" {
		t.Fatalf("rule after identity deletion = %q", got)
	}
	if err := reopened.DeleteGitProfile("github.com/acme/app"); err != nil {
		t.Fatal(err)
	}
	if len(reopened.GitProfiles()) != 1 {
		t.Fatal("legacy key delete did not remove whole key")
	}
	if got := effectiveName(t, other); got != "Global Person" {
		t.Fatalf("rule after key deletion = %q", got)
	}
}

func TestLegacyGitIdentityIDsRemainSelectable(t *testing.T) {
	_, home := isolateGit(t)
	s := profileTestService(t)
	legacy := GitProfile{Key: "github.com/acme", Name: "Legacy", Email: "legacy@example.dev"}
	second := GitProfile{Key: legacy.Key, Name: "Other", Email: "other@example.dev"}
	if err := s.store.Update(func(st *Store) { st.GitProfiles = []GitProfile{legacy, second} }); err != nil {
		t.Fatal(err)
	}
	reopened := profileTestService(t)
	if got := reopened.GitProfiles()[0].IdentityID(); got != legacy.IdentityID() || got == second.IdentityID() {
		t.Fatalf("legacy IDs unstable: %q", got)
	}
	if got := reopened.MatchGitProfileFor("git@github.com:acme/app.git"); got == nil || got.Name != legacy.Name {
		t.Fatalf("legacy first = %+v", got)
	}
	dir := makeRepo(t, home, "legacy", "git@github.com:acme/app.git")
	if err := reopened.SelectGitProfile(dir, second.IdentityID()); err != nil {
		t.Fatal(err)
	}
	if got := effectiveName(t, dir); got != second.Name {
		t.Fatalf("selected legacy identity = %q", got)
	}
	if err := reopened.DeleteGitProfile(second.IdentityID()); err != nil {
		t.Fatal(err)
	}
	if got := reopened.GitProfiles(); len(got) != 1 || got[0].IdentityID() != legacy.IdentityID() {
		t.Fatalf("legacy ID delete = %+v", got)
	}
}

func TestGitRulesActiveSpecificityAndQuotedValues(t *testing.T) {
	_, home := isolateGit(t)
	name := "Repo # ; \"quoted\" \\ path\tvalue"
	email := "quoted#tag@example.dev"
	profiles := []GitProfile{
		{Key: "github.com/acme/app", Name: "Inactive", Email: "inactive@example.dev"},
		{Key: "github.com/acme/app", Name: name, Email: email, Active: true},
		{Key: "github.com/acme", Name: "Owner", Email: "owner@example.dev"},
		{Key: "github.com", Name: "Host", Email: "host@example.dev"},
	}
	text := GitRulesText(profiles)
	if strings.Count(text, "hasconfig:remote.*.url:git@github.com:acme/app.git") != 1 {
		t.Fatalf("duplicate identity rules:\n%s", text)
	}
	hostPos := strings.Index(text, "git@github.com:*")
	ownerPos := strings.Index(text, "git@github.com:acme/**")
	repoPos := strings.Index(text, "git@github.com:acme/app/**")
	if hostPos < 0 || ownerPos <= hostPos || repoPos <= ownerPos {
		t.Fatalf("specificity order:\n%s", text)
	}
	if err := InstallGitRules(profiles); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ url, want string }{
		{"https://github.com/acme/app.git", name},
		{"git@github.com:acme/sibling.git", "Owner"},
		{"https://github.com/other/app.git", "Host"},
	} {
		dir := makeRepo(t, home, ProfileSlug(c.url), c.url)
		if got := effectiveName(t, dir); got != c.want {
			t.Errorf("%s identity = %q, want %q", c.url, got, c.want)
		}
	}
	value, err := runGit(home, "config", "--file", filepath.Join(GitConfigDir(), ProfileSlug("github.com/acme/app")+".gitconfig"), "--get", "user.email")
	if err != nil || value != email {
		t.Fatalf("quoted email = %q, %v", value, err)
	}
}
