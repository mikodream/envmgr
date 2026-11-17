package cli

import "testing"

// TestIsGitAction pins which words are subcommands. It matters because a project
// can be named anything: if every bare word were a subcommand, `envmgr git cf`
// would fail on the project called cf.
func TestIsGitAction(t *testing.T) {
	for _, s := range []string{"status", "init", "set", "profile", "rules"} {
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
