package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func syncStore(t *testing.T, path string) *Store {
	t.Helper()
	s := &Store{path: path, Config: DefaultConfig()}
	if err := s.reloadLocked(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStateSyncFreshUpdatesAndReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, b := syncStore(t, path), syncStore(t, path)
	if err := a.Update(func(s *Store) { s.Config.Theme = "dark" }); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(func(s *Store) { s.Manual = append(s.Manual, ManualInstall{Kind: Node, Path: "node"}) }); err != nil {
		t.Fatal(err)
	}
	if a.ManualCopy()[0].Path != "node" || b.ConfigCopy().Theme != "dark" {
		t.Fatal("stale state overwrote another writer")
	}
}

func TestStateSyncSaveMergesOnlyLocalEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, b := syncStore(t, path), syncStore(t, path)
	a.Config.Theme = "dark"
	b.Config.Language = "en"
	a.Manual = append(a.Manual, ManualInstall{Kind: Node, Path: "node"})
	b.Manual = append(b.Manual, ManualInstall{Kind: Python, Path: "python"})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	c := syncStore(t, path)
	if c.Config.Theme != "dark" || c.Config.Language != "en" || len(c.Manual) != 2 {
		t.Fatalf("lost edits: %+v", c.diskState())
	}
}

func TestStateSyncFailedUpdateRollsBackNestedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := syncStore(t, path)
	if err := s.Update(func(st *Store) {
		st.Config.ScanRoots = []string{"original"}
		st.Projects = []Project{{ID: "p", Pins: map[Kind]string{Node: "20"}}}
	}); err != nil {
		t.Fatal(err)
	}
	before := s.diskState()
	// A directory at the unique destination makes rename fail on every platform.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(st *Store) { st.Config.ScanRoots[0] = "dirty"; st.Projects[0].Pins[Node] = "22" }); err == nil {
		t.Fatal("expected save failure")
	}
	if !reflect.DeepEqual(before, s.diskState()) {
		t.Fatalf("dirty memory: %+v", s.diskState())
	}
	if entries, err := filepath.Glob(filepath.Join(dir, ".state.json-*")); err != nil || len(entries) != 0 {
		t.Fatalf("temporary files: %v %v", entries, err)
	}
}

func TestStateSyncFailedCommitDiscardsCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := syncStore(t, path)
	if err := s.Update(func(st *Store) { st.Config.Theme = "dark" }); err != nil {
		t.Fatal(err)
	}
	err := s.Update(func(st *Store) {
		st.Config.Theme = "dirty"
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil || s.Config.Theme != "dark" {
		t.Fatalf("err=%v config=%+v", err, s.Config)
	}
}

func TestStateSyncProjectFileIsAuthoritative(t *testing.T) {
	dir := t.TempDir()
	p := NewProject(dir)
	p.Pins = map[Kind]string{Node: "old"}
	if err := UpdateProjectPins(dir, map[Kind]string{Node: "20", Python: "3.12"}); err != nil {
		t.Fatal(err)
	}
	for _, follow := range []bool{true, false} {
		got := Resolve(p, nil, follow)
		if len(got) != 2 || got[0].Expr != "20" {
			t.Fatalf("stale pins: %+v", got)
		}
	}
	if err := UpdateProjectPins(dir, map[Kind]string{Node: "22", Python: ""}); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(p, nil, false); len(got) != 1 || got[0].Expr != "22" {
		t.Fatalf("changed config: %+v", got)
	}
	if err := os.Remove(filepath.Join(dir, ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(p, nil, false); len(got) != 0 {
		t.Fatalf("deleted config resurrected pins: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(p, nil, false); len(got) != 1 || got[0].Err == nil {
		t.Fatalf("invalid config hidden: %+v", got)
	}
}

func TestStateSyncProjectConcurrentMerge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"extra":{"keep":true},"pins":{"ruby":"3.3"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, kind := range Kinds {
		wg.Add(1)
		go func(k Kind) {
			defer wg.Done()
			if err := UpdateProjectPins(dir, map[Kind]string{k: "1.2.3"}); err != nil {
				t.Error(err)
			}
		}(kind)
	}
	wg.Wait()
	pins, err := ReadProjectPins(dir)
	if err != nil || len(pins) != len(Kinds) {
		t.Fatalf("pins=%v err=%v", pins, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(b, &file); err != nil || file["extra"] == nil {
		t.Fatalf("extra field lost: %s %v", b, err)
	}
}

func TestStateSyncProjectReadsRemainAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := SetProjectPin(dir, Node, "20"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				pins, err := ReadProjectPins(dir)
				if err != nil || pins[Node] == "" {
					t.Errorf("partial project config: %v %v", pins, err)
					return
				}
			}
		}
	}()
	for i := 0; i < 24; i++ {
		if err := SetProjectPin(dir, Node, strconv.Itoa(i+20)); err != nil {
			t.Error(err)
			break
		}
	}
	close(done)
	wg.Wait()
}

func TestStateSyncSetPinFailureKeepsStoreClean(t *testing.T) {
	dir := t.TempDir()
	s := syncStore(t, filepath.Join(t.TempDir(), "state.json"))
	p := NewProject(dir)
	if err := s.Update(func(st *Store) { st.Projects = []Project{p} }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: s}
	if err := svc.SetPin(p.ID, Node, "22"); err == nil {
		t.Fatal("expected config error")
	}
	if s.Projects[0].Pins[Node] != "" {
		t.Fatal("failed pin left dirty store")
	}
}

func TestStateSyncProcessWorker(t *testing.T) {
	path := os.Getenv("ENVMGR_SYNC_WORKER")
	if path == "" {
		return
	}
	id := os.Getenv("ENVMGR_SYNC_ID")
	if id == "crash" {
		lockErr := withFileLock(path, func() error { os.Exit(0); return nil })
		t.Fatal(lockErr)
	}
	index, err := strconv.Atoi(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateProjectPins(filepath.Dir(path), map[Kind]string{Kinds[index]: "1.2.3"}); err != nil {
		t.Fatal(err)
	}
	s := syncStore(t, path)
	for i := 0; i < 12; i++ {
		if err := s.Update(func(st *Store) {
			st.Manual = append(st.Manual, ManualInstall{Kind: Node, Path: id + "/" + strconv.Itoa(i)})
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStateSyncCrossProcessAndCrashRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	command := func(id string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStateSyncProcessWorker$")
		cmd.Env = append(os.Environ(), "ENVMGR_SYNC_WORKER="+path, "ENVMGR_SYNC_ID="+id)
		return cmd
	}
	if out, err := command("crash").CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v %s", err, out)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if out, err := command(fmt.Sprint(id)).CombinedOutput(); err != nil {
				t.Errorf("worker %d: %v %s", id, err, out)
			}
		}(i)
	}
	wg.Wait()
	if got := len(syncStore(t, path).Manual); got != 48 {
		t.Fatalf("lost process updates: %d, want 48", got)
	}
	if pins, err := ReadProjectPins(filepath.Dir(path)); err != nil || len(pins) != 4 {
		t.Fatalf("lost process pins: %v %v", pins, err)
	}
}

func TestStateSyncReadErrorReachesEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := syncStore(t, path)
	if err := s.Update(func(st *Store) { st.Config.Theme = "dark" }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: s}
	got := svc.Resolve(NewProject(t.TempDir()))
	if len(got) != 1 || got[0].Err == nil || got[0].Source != "state.json" {
		t.Fatalf("hidden state error: %+v", got)
	}
	if s.ReadError() == nil || s.Config.Theme != "dark" {
		t.Fatal("expected visible error and last good state")
	}
}

func TestStateSyncFailedSaveRestoresMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := syncStore(t, path)
	if err := s.Update(func(st *Store) { st.Config.ScanRoots = []string{"original"} }); err != nil {
		t.Fatal(err)
	}
	s.Config.ScanRoots[0] = "dirty"
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err == nil {
		t.Fatal("expected failure")
	}
	if s.Config.ScanRoots[0] != "original" {
		t.Fatalf("dirty save state: %+v", s.Config)
	}
}
