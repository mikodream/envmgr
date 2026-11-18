package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureCLIOutput(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer func() { os.Stdout = original }()
	os.Stdout = w
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, r)
		done <- err
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestGitUsageDocumentsRemotesAndIdentitySelection(t *testing.T) {
	var out bytes.Buffer
	PrintUsage(&out)
	for _, text := range []string{"git remote list", "git remote add", "git remote rm", "git profile use ID --dir", "git profile edit ID", "身份 ID"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("usage missing %q", text)
		}
	}
}

func TestFlagValueKeepsRemotePositionals(t *testing.T) {
	for _, args := range [][]string{
		{"remote", "add", "origin", "git@github.com:acme/app.git", "--dir", "project"},
		{"--dir=project", "remote", "add", "origin", "git@github.com:acme/app.git"},
	} {
		dir, rest := flagValue(args, "dir", "")
		if dir != "project" || strings.Join(rest, "|") != "remote|add|origin|git@github.com:acme/app.git" {
			t.Errorf("flagValue(%v) = %q, %v", args, dir, rest)
		}
	}
}

// TestIsGitAction pins which words are subcommands. It matters because a project
// can be named anything: if every bare word were a subcommand, `envmgr git cf`
// would fail on the project called cf.
func TestIsGitAction(t *testing.T) {
	for _, s := range []string{"status", "init", "set", "remote", "profile", "rules"} {
		if !isGitAction(s) {
			t.Errorf("isGitAction(%q) = false, want true", s)
		}
	}
	// Project names and flags must not be mistaken for subcommands. "profiles"
	// and "rule" are near misses that must stay project names.
	for _, s := range []string{
		"cf", "app", "my-project", "--clear", "", "Status", "profiles", "rule", "rules2",
	} {
		if isGitAction(s) {
			t.Errorf("isGitAction(%q) = true, want false", s)
		}
	}
}
