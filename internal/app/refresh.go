package app

import (
	"sync"

	"envmgr/internal/core"
)

// updateQueue coalesces service notifications while a snapshot is loading or
// waiting for the UI thread. Notifications consume cached state; only the
// explicit refresh action starts SDK discovery.
type updateQueue struct {
	mu      sync.Mutex
	running bool
	pending bool
	load    func() func()
	post    func(func())
}

func (q *updateQueue) request() {
	q.mu.Lock()
	if q.running {
		q.pending = true
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()
	go q.run()
}

func (q *updateQueue) run() {
	apply := q.load()
	q.post(func() {
		apply()
		q.mu.Lock()
		again := q.pending
		q.pending = false
		if !again {
			q.running = false
		}
		q.mu.Unlock()
		if again {
			go q.run()
		}
	})
}

// watchService keeps SDK state notifications separate from Git-only redraws.
// post and invalidate are captured once when the window is attached.
func (a *App) watchService(post func(func()), invalidate func()) {
	q := &updateQueue{load: a.serviceUpdate, post: post}
	a.serviceSync = q
	a.svc.OnChange(q.request)
	a.svc.OnGitChange(invalidate)
}

// requestServiceUpdate refreshes page facts after navigation or an explicit
// change. Drawing and wheel animation never call this method.
func (a *App) requestServiceUpdate() {
	if a.serviceSync != nil {
		a.serviceSync.request()
	}
}

// serviceUpdate builds derived state off the UI thread. The posted closure only
// swaps ready-to-render values; it performs no discovery, filesystem checks, or
// Git commands while wheel frames are being drawn.
func (a *App) serviceUpdate() func() {
	installed := a.svc.CachedInstalled()
	projects := a.svc.Projects()
	managers := a.svc.Managers()
	follow := a.svc.Config().FollowMarkers
	resolutions := make(map[string][]core.Resolution, len(projects))
	for _, project := range projects {
		resolutions[project.ID] = core.Resolve(project, installed, follow)
	}
	report := core.DiagnoseWithInstalled(a.svc, installed)
	inventory := buildInventoryView(installed)
	doctor := buildDoctorView(report)
	settings := loadSettingsSnapshot(a.svc)
	return func() {
		a.inventory = inventory
		a.doctor = doctor
		a.settings = settings
		a.installed = installed
		a.projects = projects
		a.managers = managers
		a.resolutions = resolutions
		a.report = report
		a.syncedAt = nowLabel()
		if len(a.pinBuf) != len(core.Kinds) {
			a.pinBuf = make([]string, len(core.Kinds))
		}
	}
}
