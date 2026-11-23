package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A shell in src/ has to see the versions its project root declares: that is
// what nvm and pyenv do, and what makes the cd hook usable inside a monorepo.
func TestNearestProjectDirWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".nvmrc"), []byte("20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "src", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := NearestProjectDir(nested)
	if !ok || filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("nearest = %q, ok = %v; want %q", got, ok, root)
	}

	// A nested package that declares its own version wins over its parent.
	src := filepath.Join(root, "src")
	if err := os.WriteFile(filepath.Join(src, ".python-version"), []byte("3.12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok = NearestProjectDir(nested)
	if !ok || filepath.Clean(got) != filepath.Clean(src) {
		t.Fatalf("nested nearest = %q, ok = %v; want %q", got, ok, src)
	}
}

func TestNearestProjectDirWithoutDeclaration(t *testing.T) {
	dir := t.TempDir()
	// .git makes a repository, not a version declaration. Climbing past it
	// would let a parent's file decide versions for an unrelated project.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := NearestProjectDir(nested); ok {
		t.Fatalf("found %q without a version declaration", got)
	}
}

func TestEnvForSubdirectoryInheritsProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "work", "app")
	sub := filepath.Join(root, "internal", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectConfig(root, map[Kind]string{Node: "99999.0.0"}); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	env := svc.EnvForDir(sub)
	if len(env.Issues) == 0 || !strings.Contains(env.Issues[0], "99999.0.0") {
		t.Fatalf("subdirectory did not inherit the project's pin: %+v", env)
	}
}
