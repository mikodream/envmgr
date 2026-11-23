package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The shell hook only needs the last full scan. A fresh snapshot must be
// reused verbatim, with no discovery and therefore no subprocess per SDK.
func TestFastServiceReusesFreshSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	nodeDir := filepath.Join(home, "elsewhere", "node", "20.11.1")
	bin := filepath.Join(nodeDir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saveDiscoverySnapshot([]Installed{{
		Kind: Node, Version: "20.11.1", Path: nodeDir, Manager: "手动",
		BinDirs: []string{bin}, FoundAt: time.Now(),
	}}, time.Now())

	svc, err := NewFastService()
	if err != nil {
		t.Fatal(err)
	}
	list := svc.CachedInstalled()
	if len(list) != 1 || list[0].Version != "20.11.1" || list[0].Path != nodeDir {
		t.Fatalf("cached installs = %+v", list)
	}
}

// A snapshot older than the TTL is not trusted: the service scans again and
// replaces it, so a version installed by another tool still shows up.
func TestFastServiceScansWhenSnapshotIsStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := filepath.Join(home, "elsewhere", "node", "20.11.1")
	if err := os.MkdirAll(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	saveDiscoverySnapshot([]Installed{{
		Kind: Node, Version: "20.11.1", Path: fake, Manager: "手动",
	}}, time.Now().Add(-2*DiscoveryCacheTTL))

	svc, err := NewFastService()
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range svc.CachedInstalled() {
		if inst.Path == fake {
			t.Fatalf("stale snapshot was trusted: %+v", inst)
		}
	}

	snap, ok := loadDiscoverySnapshot()
	if !ok || time.Since(snap.ScannedAt) > 10*time.Second {
		t.Fatalf("stale snapshot was not replaced: ok=%v at=%v", ok, snap.ScannedAt)
	}
}
