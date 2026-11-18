package app

import (
	"sync/atomic"
	"testing"
	"time"
)

func awaitUpdate(t *testing.T, updates <-chan func()) func() {
	t.Helper()
	select {
	case apply := <-updates:
		return apply
	case <-time.After(10 * time.Second):
		t.Fatal("state update did not reach UI scheduler")
		return nil
	}
}

func TestServiceNotificationDoesNotStartAnotherSDKScan(t *testing.T) {
	a := newTestApp(t)
	updates := make(chan func(), 4)
	a.watchService(func(apply func()) { updates <- apply }, func() {})
	scanned := a.svc.ScannedAt()
	a.svc.Notify()
	awaitUpdate(t, updates)()
	if !a.svc.ScannedAt().Equal(scanned) {
		t.Fatal("a state notification started SDK discovery again")
	}
	select {
	case <-updates:
		t.Fatal("applying the update fed another notification back into the scheduler")
	default:
	}
}

func TestUpdateQueueCoalescesWhileLoadingAndWaitingForUI(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	updates := make(chan func(), 4)
	var loads, applied atomic.Int32
	q := &updateQueue{
		load: func() func() {
			if loads.Add(1) == 1 {
				close(started)
				<-release
			}
			return func() { applied.Add(1) }
		},
		post: func(apply func()) { updates <- apply },
	}
	q.request()
	<-started
	for i := 0; i < 100; i++ {
		q.request()
	}
	close(release)
	first := awaitUpdate(t, updates)
	for i := 0; i < 100; i++ {
		q.request()
	}
	if loads.Load() != 1 {
		t.Fatal("UI queue accumulated concurrent snapshot jobs")
	}
	first()
	awaitUpdate(t, updates)()
	if loads.Load() != 2 || applied.Load() != 2 {
		t.Fatalf("loads=%d applied=%d, want one initial and one coalesced update", loads.Load(), applied.Load())
	}
	q.mu.Lock()
	running, pending := q.running, q.pending
	q.mu.Unlock()
	if running || pending {
		t.Fatal("queue did not become idle after updates were applied")
	}
}
