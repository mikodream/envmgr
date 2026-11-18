package app

import (
	"fmt"
	"testing"
	"time"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

// Use real wheel events (smooth animation), rather than touchpad Scroll or a
// one-frame PNG. The notification feedback is covered separately by the queue
// tests, because the headless tester does not run Window.Update's native queue.
func TestProjectPagesHandleRepeatedWheelBursts(t *testing.T) {
	a := newTestApp(t)
	home := t.TempDir()
	// Share a cached directory while giving the gallery distinct project IDs.
	// This keeps the test about scrolling, without forty real Git subprocesses.
	_ = a.svc.Git(home)
	a.projects = nil
	a.resolutions = map[string][]core.Resolution{}
	for i := 0; i < 40; i++ {
		p := core.NewProject(home)
		p.ID = fmt.Sprintf("wheel-%02d", i)
		p.Name = fmt.Sprintf("Project-%02d", i)
		a.projects = append(a.projects, p)
	}
	a.page = PageProjects
	tt := ui.NewTester(a.View, 1180, 780)
	first, ok := tt.Find("Project-00")
	if !ok {
		t.Fatal("first project is missing before scrolling")
	}
	burstWheel(tt, 120)
	moved, ok := tt.Find("Project-00")
	if ok && moved.Y >= first.Y {
		t.Fatal("gallery did not scroll down")
	}
	burstWheel(tt, -120)
	returned, ok := tt.Find("Project-00")
	if !ok || returned.Y != first.Y {
		t.Fatalf("gallery did not scroll back: first=%+v after=%+v found=%v", first, returned, ok)
	}
	if tt.FramePending() {
		t.Fatal("gallery wheel animation never settled")
	}

	p := a.projects[0]
	for _, kind := range core.Kinds {
		a.resolutions[p.ID] = append(a.resolutions[p.ID], core.Resolution{Kind: kind, Expr: "1.0.0", Missing: true})
	}
	a.OpenProject(0, true)
	detail := ui.NewTester(a.View, 1180, 780)
	title, ok := detail.Find(p.Path)
	if !ok {
		t.Fatal("project detail header is missing")
	}
	burstWheel(detail, 120)
	scrolled, ok := detail.Find(p.Path)
	if ok && scrolled.Y >= title.Y {
		t.Fatal("project configuration did not scroll down")
	}
	burstWheel(detail, -120)
	restored, ok := detail.Find(p.Path)
	if !ok || restored.Y != title.Y {
		t.Fatalf("project configuration did not scroll back: before=%+v after=%+v", title, restored)
	}
	if detail.FramePending() {
		t.Fatal("project configuration wheel animation never settled")
	}
}

func burstWheel(tt *ui.Tester, dy float32) {
	tt.Move(900, 450)
	for i := 0; i < 30; i++ {
		tt.Wheel(900, 450, 0, dy)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Advance(time.Second)
}
