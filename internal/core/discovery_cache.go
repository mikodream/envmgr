package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DiscoveryCacheTTL is how long a shell may reuse the last full discovery.
//
// The cd hook asks for an environment on every directory change. A full scan
// runs each SDK's own binary to learn its version, which costs a couple of
// hundred milliseconds; that is fine once, not on every `cd src`. A short
// snapshot keeps the interactive path fast while still noticing versions
// installed by other tools within a minute.
const DiscoveryCacheTTL = 60 * time.Second

// discoverySnapshotVersion guards the file format. Bump it when Installed
// changes in a way old files cannot express.
const discoverySnapshotVersion = 1

// discoverySnapshot is the last full scan, written next to state.json so a
// short-lived CLI process can answer without discovering anything.
type discoverySnapshot struct {
	Version   int         `json:"version"`
	ScannedAt time.Time   `json:"scannedAt"`
	Installs  []Installed `json:"installs"`
}

// DiscoverySnapshotPath is where the last scan is cached.
func DiscoverySnapshotPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "discovery.json"), nil
}

func loadDiscoverySnapshot() (discoverySnapshot, bool) {
	path, err := DiscoverySnapshotPath()
	if err != nil {
		return discoverySnapshot{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return discoverySnapshot{}, false
	}
	var snap discoverySnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return discoverySnapshot{}, false
	}
	if snap.Version != discoverySnapshotVersion || snap.ScannedAt.IsZero() {
		return discoverySnapshot{}, false
	}
	return snap, true
}

func saveDiscoverySnapshot(list []Installed, at time.Time) {
	path, err := DiscoverySnapshotPath()
	if err != nil {
		return
	}
	b, err := json.Marshal(discoverySnapshot{
		Version:   discoverySnapshotVersion,
		ScannedAt: at,
		Installs:  list,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// A cache that cannot be written is not an error: the next process simply
	// pays for a full scan again.
	_ = atomicWriteFile(path, b, 0o644)
}

// hydrateDiscovery fills the cache from the last scan, or scans when the
// snapshot is missing or older than DiscoveryCacheTTL. Paths that have
// disappeared since the scan are dropped, so a deleted version cannot leak
// into an exported PATH.
func (s *Service) hydrateDiscovery() {
	snap, ok := loadDiscoverySnapshot()
	if !ok || time.Since(snap.ScannedAt) > DiscoveryCacheTTL {
		s.Refresh()
		return
	}
	list := make([]Installed, 0, len(snap.Installs))
	for _, inst := range snap.Installs {
		if inst.Available() {
			list = append(list, inst)
		}
	}
	s.mu.Lock()
	s.installed = list
	s.scanned = time.Now()
	s.mu.Unlock()
}
