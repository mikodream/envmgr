package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A version a project still pins must not disappear from under it.
func TestRemoveRefusesVersionStillPinned(t *testing.T) {
	home := t.TempDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	t.Setenv("HOME", home)

	sdk := filepath.Join(home, ".envmgr", "sdk", "go", "1.2.3")
	if err := os.MkdirAll(filepath.Join(sdk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "VERSION"), []byte("go1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "bin", "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	project := filepath.Join(home, "work", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectConfig(project, map[Kind]string{Go: "1.2.3"}); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddProject(project); err != nil {
		t.Fatal(err)
	}
	inst, ok := Exact(svc.Installed(), Go, "1.2.3")
	if !ok {
		t.Fatalf("installed = %+v", svc.Installed())
	}

	users := svc.VersionUsers(Go, "1.2.3")
	if len(users) != 1 || !SamePath(users[0].Path, project) {
		t.Fatalf("users = %+v", users)
	}

	err = svc.Remove(inst)
	var inUse *VersionInUseError
	if !errors.As(err, &inUse) || len(inUse.Projects) != 1 {
		t.Fatalf("Remove error = %v; want VersionInUseError with one project", err)
	}
	if _, statErr := os.Stat(sdk); statErr != nil {
		t.Fatalf("refused removal still deleted the directory: %v", statErr)
	}

	if err := svc.RemoveForced(inst); err != nil {
		t.Fatalf("forced removal: %v", err)
	}
	if _, err := os.Stat(sdk); !os.IsNotExist(err) {
		t.Fatalf("forced removal left %s behind: %v", sdk, err)
	}
}
