package app

import (
	"testing"

	"envmgr/internal/core"
)

// newTestApp builds an app without touching the network or the real store.
func newTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	svc, err := core.NewService()
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return New(svc)
}

// fakeRemote is a short list for one kind, enough to tell lists apart.
func fakeRemote(kind core.Kind, versions ...string) []core.Remote {
	out := make([]core.Remote, 0, len(versions))
	for _, v := range versions {
		out = append(out, core.Remote{Kind: kind, Version: v, LTS: true})
	}
	return out
}

// TestRemoteActionFetchesOnTabSwitch is the regression: switching the SDK tab
// used to leave the page on the previous kind's data, so the new tab looked
// empty and never fetched.
func TestRemoteActionFetchesOnTabSwitch(t *testing.T) {
	a := newTestApp(t)

	// First visit: nothing known about Node yet.
	a.downloadKind = kindIndexOf(core.Node)
	if got := a.remoteActionFor(core.Node); got != remoteFetch {
		t.Fatalf("fresh Node: action = %v, want remoteFetch", got)
	}

	// The fetch lands.
	a.remote = fakeRemote(core.Node, "24.13.0")
	a.remoteKind = core.Node
	a.remoteLoaded = true
	if got := a.remoteActionFor(core.Node); got != remoteShowList {
		t.Fatalf("loaded Node: action = %v, want remoteShowList", got)
	}

	// Switching to Python must not reuse Node's answer.
	if got := a.remoteActionFor(core.Python); got != remoteFetch {
		t.Fatalf("after switching to Python: action = %v, want remoteFetch", got)
	}

	// And Python's own list is shown once fetched.
	a.remote = fakeRemote(core.Python, "3.14.3")
	a.remoteKind = core.Python
	a.remoteLoaded = true
	if got := a.remoteActionFor(core.Python); got != remoteShowList {
		t.Fatalf("loaded Python: action = %v, want remoteShowList", got)
	}
	if rows := a.visibleRemote(core.Python); len(rows) != 1 || rows[0].Version != "3.14.3" {
		t.Fatalf("visibleRemote(Python) = %v, want the Python list", rows)
	}
}

// TestRemoteActionUsesCacheWhenComingBack covers the second half: going back to
// a tab that was already fetched should not refetch it.
func TestRemoteActionUsesCacheWhenComingBack(t *testing.T) {
	a := newTestApp(t)
	a.cacheRemote(core.Java, fakeRemote(core.Java, "21.0.9", "17.0.17"))
	a.remote = fakeRemote(core.Node, "24.13.0")
	a.remoteKind = core.Node
	a.remoteLoaded = true

	if got := a.remoteActionFor(core.Java); got != remoteUseCache {
		t.Fatalf("Java after Node: action = %v, want remoteUseCache", got)
	}
	a.useCachedRemote(core.Java)
	if a.remoteKind != core.Java || !a.remoteLoaded {
		t.Fatalf("useCachedRemote left kind=%v loaded=%v", a.remoteKind, a.remoteLoaded)
	}
	if rows := a.visibleRemote(core.Java); len(rows) != 2 {
		t.Fatalf("visibleRemote(Java) = %d rows, want 2", len(rows))
	}
}

// TestRemoteActionKeepsErrorVisible makes sure a failed load does not turn into
// a fetch loop on every frame, and that 重试 can still force one.
func TestRemoteActionKeepsErrorVisible(t *testing.T) {
	a := newTestApp(t)
	a.remoteKind = core.Rust
	a.remoteLoaded = true
	a.remoteErr = "network unreachable"

	if got := a.remoteActionFor(core.Rust); got != remoteShowError {
		t.Fatalf("failed load: action = %v, want remoteShowError", got)
	}
	// ensureRemote must not start a fetch again on its own.
	a.ensureRemote(core.Rust)
	if a.remoteLoading {
		t.Fatal("ensureRemote started a retry; the error card would flicker forever")
	}
}

// TestRemoteActionWaitsForInFlight covers the spinner: no second fetch while
// the first is still running.
func TestRemoteActionWaitsForInFlight(t *testing.T) {
	a := newTestApp(t)
	a.remoteLoading = true
	a.remoteKind = core.Go
	if got := a.remoteActionFor(core.Go); got != remoteWait {
		t.Fatalf("in flight: action = %v, want remoteWait", got)
	}
}

// TestEnsureRemoteDoesNotFetchForOtherTabs guards against the page fetching a
// kind it is not showing.
func TestEnsureRemoteDoesNotFetchForOtherTabs(t *testing.T) {
	a := newTestApp(t)
	a.remote = fakeRemote(core.Ruby, "3.3.6")
	a.remoteKind = core.Ruby
	a.remoteLoaded = true

	a.ensureRemote(core.Ruby)
	if a.remoteLoading {
		t.Fatal("ensureRemote refetched a kind that was already loaded")
	}
	if len(a.remote) != 1 || a.remote[0].Kind != core.Ruby {
		t.Fatalf("ensureRemote replaced the list: %v", a.remote)
	}
}

// TestLTSFilterKeepsKindSeparate makes sure the list stays filtered by kind
// after a tab switch, which is what made an empty list look like a bug.
func TestLTSFilterKeepsKindSeparate(t *testing.T) {
	a := newTestApp(t)
	a.remote = fakeRemote(core.Node, "24.13.0", "18.16.0")
	a.remoteKind = core.Node
	a.remoteLoaded = true

	a.filterLTS = true
	if rows := a.visibleRemote(core.Python); len(rows) != 0 {
		t.Fatalf("visibleRemote(Python) while Node is loaded = %v, want empty", rows)
	}
	if rows := a.visibleRemote(core.Node); len(rows) != 2 {
		t.Fatalf("visibleRemote(Node) = %d rows, want 2", len(rows))
	}
}
