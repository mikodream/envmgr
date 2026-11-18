package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitGitCache(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Git cache operation")
	}
}

func TestGitCacheConcurrentRefreshSingleFlight(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "Peek"
		if refresh {
			name = "PeekAndRefresh"
		}
		t.Run(name, func(t *testing.T) {
			const dir = "test-repository"
			var loads, sdkChanges, gitChanges atomic.Int32
			started := make(chan struct{}, 100)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			s := &Service{gitLoad: func(got string) GitInfo {
				loads.Add(1)
				started <- struct{}{}
				<-release
				return GitInfo{Repo: true, Root: got, Branch: "loaded"}
			}}
			s.OnChange(func() { sdkChanges.Add(1) })
			notified := make(chan struct{}, 100)
			s.OnGitChange(func() {
				// Both methods acquire s.mu: callbacks must run after it is released.
				s.PeekGit(dir)
				s.OnGitChange(func() {})
				gitChanges.Add(1)
				notified <- struct{}{}
			})

			start := make(chan struct{})
			var callers sync.WaitGroup
			for i := range 100 {
				callers.Add(1)
				go func() {
					defer callers.Done()
					<-start
					if refresh && i%2 == 0 {
						s.RefreshGit(dir)
					} else {
						s.PeekGit(dir)
					}
				}()
			}
			close(start)
			callers.Wait()
			waitGitCache(t, started)
			if got := loads.Load(); got != 1 {
				t.Fatalf("loads while blocked = %d, want 1", got)
			}
			if got := sdkChanges.Load(); got != 0 {
				t.Fatalf("SDK notifications while blocked = %d", got)
			}
			if got := gitChanges.Load(); got != 0 {
				t.Fatalf("Git notifications while blocked = %d", got)
			}
			unblock()
			waitGitCache(t, notified)
			if got := loads.Load(); got != 1 {
				t.Errorf("loads = %d, want 1", got)
			}
			if got := gitChanges.Load(); got != 1 {
				t.Errorf("Git notifications = %d, want 1", got)
			}
			if got := sdkChanges.Load(); got != 0 {
				t.Errorf("SDK notifications = %d, want 0", got)
			}
			info, ok := s.PeekGit(dir)
			if !ok || info.Root != dir || info.Branch != "loaded" {
				t.Errorf("cached result = %+v, %v", info, ok)
			}
		})
	}
}

func TestGitCacheRefreshPreservesStaleResult(t *testing.T) {
	const dir = "stale-repository"
	var loads atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	s := &Service{
		gitCache: map[string]gitCacheEntry{dir: {
			info: GitInfo{Repo: true, Branch: "stale"},
			at:   time.Now().Add(-2 * gitCacheTTL),
		}},
		gitLoad: func(string) GitInfo {
			if loads.Add(1) == 1 {
				close(started)
			}
			<-release
			return GitInfo{Repo: true, Branch: "fresh"}
		},
	}
	notified := make(chan struct{}, 100)
	s.OnGitChange(func() { notified <- struct{}{} })
	s.RefreshGit(dir)
	waitGitCache(t, started)
	for range 100 {
		info, ok := s.PeekGit(dir)
		if !ok || info.Branch != "stale" {
			t.Fatalf("pending peek = %+v, %v; want stale cache", info, ok)
		}
		if info := s.Git(dir); info.Branch != "stale" {
			t.Fatalf("pending Git = %+v; want stale cache", info)
		}
		s.RefreshGit(dir)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loads while refresh pending = %d, want 1", got)
	}
	unblock()
	waitGitCache(t, notified)
	if info, ok := s.PeekGit(dir); !ok || info.Branch != "fresh" {
		t.Errorf("completed peek = %+v, %v; want fresh cache", info, ok)
	}
}

func TestGitCacheReloadUsesInjectedLoader(t *testing.T) {
	var loads int
	s := &Service{gitLoad: func(dir string) GitInfo {
		loads++
		return GitInfo{Repo: true, Root: dir, Branch: "injected"}
	}}
	info := s.Git("injected-repository")
	if info.Root != "injected-repository" || info.Branch != "injected" {
		t.Fatalf("first Git result = %+v", info)
	}
	if cached, ok := s.PeekGit("injected-repository"); !ok || cached.Branch != "injected" {
		t.Fatalf("cached reload = %+v, %v", cached, ok)
	}
	if loads != 1 {
		t.Errorf("loader calls = %d, want 1", loads)
	}
}

func TestCachedInstalledNeverScansWhenOldOrEmpty(t *testing.T) {
	for _, at := range []time.Time{{}, time.Now().Add(-2 * time.Hour)} {
		// A missing store deliberately makes any accidental discovery fail.
		s := &Service{scanned: at, installed: []Installed{{Version: "cached"}}}
		got := s.CachedInstalled()
		if len(got) != 1 || got[0].Version != "cached" || !s.ScannedAt().Equal(at) {
			t.Fatal("cache read changed discovery state")
		}
		got[0].Version = "modified"
		if s.CachedInstalled()[0].Version != "cached" {
			t.Fatal("cache read exposed mutable slice")
		}
	}
}

func TestEmptyInstalledUsesValidCache(t *testing.T) {
	for _, list := range [][]Installed{nil, {}} {
		at := time.Now()
		// No store is needed: a valid empty cache must never enter discovery.
		s := &Service{installed: list, scanned: at}
		var notifications int
		s.OnChange(func() { notifications++ })
		for range 100 {
			if got := s.Installed(); len(got) != 0 {
				t.Fatalf("Installed = %+v, want empty", got)
			}
		}
		if got := s.ScannedAt(); !got.Equal(at) {
			t.Errorf("scan timestamp changed: got %v, want %v", got, at)
		}
		if notifications != 0 {
			t.Errorf("discovery notifications = %d, want 0", notifications)
		}
	}
}
