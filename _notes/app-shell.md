# MyGo v0.9.2 — App Shell, Lifecycle, Desktop Integration, Headless Verification

Verified against `_probe\mygo\github.com\egoist\mygo@v0.9.2` (read-only) from scratch module
`_probe\scratch-shell`. Every signature was confirmed by `go doc`/source read, then **compile-proven**
(`go build ./...`, `go vet ./...`, `go test ./...` all exit 0). Headless PNGs produced and visually
inspected (§7). Module needs **go >= 1.27.3**; default `GOTOOLCHAIN=auto` upgrades a 1.25.x toolchain
to go1.27.4 by itself.

## 1. Lifecycle & threading

`mygo.App` is a package-level var of type `*mygo.Application` — you never construct it.

```go
func main() {
	if !mygo.App.RequestSingleInstanceLock() { return }   // FIRST, before anything
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		if w := currentWindow(); w != nil { w.Restore(); w.Show(); w.Focus() }
	})
	mygo.App.SetName("ProbeShell")   // before Run: picks userData/cache/logs dir + lock name
	mygo.App.SetVersion("0.1.0")
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	mygo.App.SetMenu(appMenu())      // before Run, to appear at launch
	mygo.App.OnWindowAllClosed(func() { if runtime.GOOS != "darwin" { mygo.App.Quit() } })
	mygo.App.OnActivate(func(hasVisibleWindows bool) { if !hasVisibleWindows { openMainWindow() } })
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) { /* e.SessionEnding => save synchronously */ })
	mygo.App.OnWillQuit(func(e *mygo.QuitEvent) { saveSettings() })
	mygo.App.WhenReady(func() { openMainWindow() })          // fires at once if already ready
	if err := mygo.App.Run(); err != nil { log.Fatal(err) }  // MUST be the main goroutine
}
```

Every listener returns an `off func()` that removes it. The events, all on `*Application`:
`WhenReady(fn)` (fires at once if already ready) · `OnReady` · `OnActivate(fn func(hasVisibleWindows
bool))` (Dock-icon click) · `OnDidBecomeActive` · `OnDidResignActive` · `OnWindowCreated(fn func(w
*Window))` · `OnWindowAllClosed(fn)` (**an AllClosed listener suppresses the default quit**) ·
`OnBeforeQuit(fn func(e *QuitEvent))` and `OnWillQuit(fn func(e *QuitEvent))` (`e.PreventDefault()`
cancels unless `e.SessionEnding`) · `OnQuitCanceled(fn)` (clear any "quitting" state you set) ·
`OnQuit(fn)` (loop stopped, just before `Run` returns) · `OnSecondInstance(fn func(args []string,
workingDir string))`.

Quit: `App.Quit()` (cancellable), `App.Exit(code)` (immediate, no events), `App.Relaunch(args...)`.
Also `App.IsReady()`, `Focus()`, `IsActive()`, `Hide()/Show()`, `SetBadgeCount(n)`, `Locale()`,
`PreferredLanguages()`, `SetLocale(tag)`, `Name()`, `Version()`, `IsPackaged()`, `mygo.IsDev()`,
`ShowAboutPanel(mygo.AboutPanelOptions{...})`, `SetOpenAtLogin(bool) error`, `OpenAtLogin()`,
`WasOpenedAtLogin()`, `AddRecentDocument`, `RecentDocuments`, `OnOpenURL`, `OnOpenFile`,
`RegisterURLScheme/Unregister/IsURLSchemeRegistered`, `App.JumpList.SetTasks([]mygo.JumpListTask{...})`.
Activation policy: `ActivationPolicyRegular` (Dock icon + menu bar), `ActivationPolicyAccessory` (no
Dock icon, windows still show), `ActivationPolicyProhibited`.

**Threading.** `Run` must be on the main goroutine. Every exported method is safe from any goroutine
(internally `onMain`/`postMain`/`await`). `WhenReady`, window events, menu `Click` and global-shortcut
callbacks run **on the main thread**, which is also the paint thread — return fast. `ui.View` build
functions run on the main thread, once per frame. `mygo.Bind` methods run on their own goroutine, one
call at a time, and may block.

### Mutating UI state from goroutines (progress bars)

There is **no `App.Post` / `App.Queue`** (grepped: absent). The primitives are:

```go
win.Update(func() { s.progress = 0.42 })   // (A) async on main thread, then rebuilds the view.
                                           //     Returns immediately; rebuilds even if nothing changed.
mygo.RunOnMain(func() {                    // (B) sync on main thread; redraws NOTHING itself.
	if n != s.done { s.done = n; win.Invalidate() }
})
win.Invalidate()                           // (C) any goroutine; coalesces into one frame.
```

`Context.Invalidate()` exists — **there is no `Context.Redraw`** (`go doc` → "no method or field"):
`func (c *Context) Invalidate()  // requests a frame from any goroutine`.
`Window.Invalidate` (`content.go:159`) drops redundant calls with `CompareAndSwap`, so per-tick use is
cheap; `Window.Update` is `content.go:190`. After `Run` returns, `RunOnMain` drops the fn (no panic).

```go
go func() {
	for i := 0; i < n; i++ {
		downloadOne(i)
		s.mu.Lock(); s.done = i + 1; s.mu.Unlock()
		win.Update(func() {})                                               // rebuild on main thread
		win.SetProgressBar(mygo.ProgressBar{Value: float64(i+1)/float64(n)}) // taskbar / Dock
	}
	win.SetProgressBar(mygo.ProgressBar{State: mygo.ProgressNone})          // clear
}()
```

`ProgressState` ∈ `ProgressNone("")`, `ProgressNormal`, `ProgressIndeterminate`, `ProgressPaused`,
`ProgressError`.

## 2. Windows

`NewWindow(opts WindowOptions) *Window` (needs the app ready; waits if called off-main) ·
`Windows() []*Window` · `WindowByID(id int) *Window` · `FocusedWindow() *Window` ·
`NewFlyout(opts FlyoutOptions) *Window`.

**`WindowOptions` — every field.** Size/placement: `Title`, `URL`, `Width, Height` (default 800×600),
`UseContentSize`, `X, Y`, `MinWidth, MinHeight`, `MaxWidth, MaxHeight`, `FitContent`, `StateKey`.
Chrome: `Hidden`, `Frameless`, `TitleBarStyle`, `TrafficLightPosition *Point`, `TitleBarHeight`,
`HideWindowButtons`, `Transparent`, `BackgroundColor`, `Vibrancy`, `VibrancyTint`, `CornerRadius`,
`Opacity`, `IgnoreMouseEvents`. Flags: `DisableResize`, `DisableMove`, `DisableMinimize`,
`DisableMaximize`, `DisableClose`, `DisableFullScreen`, `DisableShadow`, `AlwaysOnTop`, `FullScreen`,
`Maximized`, `SkipTaskbar`, `AutoHideMenuBar`. Relationship: `Parent *Window`, `Modal`, `Panel`,
`Page PageOptions`, `Content Content`.

> **Gotchas — no `Centered`, no `Resizable`, no `Show`, no `Background`.** Centering happens when `X`
> and `Y` are both zero; "resizable"/"shown" are the defaults of `DisableResize`/`Hidden`; the colour
> field is **`BackgroundColor`** (CSS: `"#1e1e1e"`, `"rgb(30 30 30)"`, `light-dark(a, b)`).

```go
win := mygo.NewWindow(mygo.WindowOptions{
	Title: "Probe Shell", Width: 900, Height: 600, MinWidth: 640, MinHeight: 420,
	StateKey: "main",                 // restores pos/size/maximized/fullscreen
	BackgroundColor: "#1e1e1e",
	Content: ui.View(s.view),         // native UI instead of a web page
})
```

`StateKey` persists to `window-state.json` in `PathUserData` on close and quit, restored only if the
geometry lands on a connected display; `App.SetWindowStateStore(WindowStateStore)` (`LoadWindowStates()
(map[string]WindowState, error)` / `SaveWindowStates(map[string]WindowState) error`) swaps the store,
`App.ClearWindowStates() error` forgets it. Methods: `Show/Hide/ShowInactive/Focus/Blur/Close/Destroy`,
`Center/Restore/Maximize/Unmaximize/ToggleMaximize/Minimize`, `SetFullScreen(bool)/ToggleFullScreen`,
`SetTitle/Title`, `SetSize/Size`, `SetContentSize/ContentSize`, `SetPosition/Position`,
`Bounds/SetBounds(Rectangle)`, `SetMinimumSize/SetMaximumSize`, `SetResizable/IsResizable`,
`SetAlwaysOnTop`, `SetOpacity`, `SetBackgroundColor(string) error`, `SetMenu(*Menu)`,
`SetProgressBar(ProgressBar)`, `SetIcon(png []byte) error`, `Invalidate()`, `Update(func())`,
`IsVisible/IsDestroyed/ID`, `NativeHandle() uintptr`, `CapturePage() ([]byte, error)` (**PNG**),
`NewWebView(WebViewOptions)`. Events, each returning `off`: `OnClose/OnClosed/OnReadyToShow/OnShow/
OnHide/OnFocus/OnBlur/OnResize/OnMove/OnMinimize/OnMaximize/OnUnmaximize/OnRestore/OnEnterFullScreen/
OnLeaveFullScreen/OnFileDrop`.

```go
win.OnClose(func(e *mygo.CloseEvent) { if s.busy() { e.PreventDefault() } })  // keeps it open
```

**Multi-window / modal-ish second window.** Any number of `NewWindow` calls; a child that blocks its
parent uses `Parent` + `Modal` (real modality: "the parent takes no input until it closes: a sheet of
the parent on macOS"):

```go
dlg := mygo.NewWindow(mygo.WindowOptions{
	Title: "Rename", Width: 380, Height: 160, Parent: win, Modal: true,
	Content: ui.View(func(c *ui.Context) { /* … */ }),
})
```

## 3. Native dialogs

`mygo.Dialog` is a var of type `DialogModule`. **All methods block until dismissed and are callable
from any goroutine** (`dialog.go:11`).

```go
func (DialogModule) Open(opts OpenDialogOptions) ([]string, error)  // empty if cancelled
func (DialogModule) Save(opts SaveDialogOptions) (string, error)    // "" if cancelled
func (DialogModule) Message(opts MessageOptions) (MessageResult, error)
func (d DialogModule) Error(title, content string)                  // no return
```

`OpenDialogOptions`: `Parent *Window`, `Title`, `DefaultPath`, `ButtonLabel`, `Message`,
`Filters []FileFilter`, `Directory`, `Multiple`, `ShowHiddenFiles`, `CreateDirectories`,
`TreatPackagesAsDirectories`. `SaveDialogOptions`: the same minus `Directory`/`Multiple`, plus
`NameFieldLabel`. `MessageOptions`: `Parent`, `Type MessageType`, `Title`, `Message`, `Detail`,
`Buttons []string` (default `["OK"]`), `DefaultButton int`, `CancelButton int`,
`DestructiveButtons []int`, `CheckboxLabel`, `CheckboxChecked`. `MessageResult`: `{ Button int;
CheckboxChecked bool }`. `MessageType` ∈ `MessageNone("")`, `MessageInfo`, `MessageWarning`,
`MessageError`, `MessageQuestion`. `FileFilter`: `{ Name string; Extensions []string; MIMETypes
[]string }` — `"*"` = all, `"image/*"` works. Dialogs (like clipboard, notifications, tray, global
shortcuts, `Theme.IsDark`) **panic before `Run`** — use them from `WhenReady` onward.

**From a button handler** — never block the frame; hand the dialog to a goroutine:

```go
if ui.Button(c, "Choose folder").Clicked() { s.chooseFolder() }   // returns at once

func (s *Shell) chooseFolder() {
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: s.win, Title: "Choose download folder", Directory: true,
			DefaultPath: s.settings.LastFolder, ButtonLabel: "Use folder",
		})
		if err != nil || len(dirs) == 0 { return }
		s.mu.Lock(); s.settings.LastFolder = dirs[0]; s.mu.Unlock()
		s.saveSettings()
		s.win.Update(func() {})            // redraw
	}()
}
res, err := mygo.Dialog.Message(mygo.MessageOptions{
	Parent: win, Type: mygo.MessageWarning, Title: "Delete 3 items?",
	Message: "This cannot be undone.", Buttons: []string{"Cancel", "Delete"},
	DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
})
if err == nil && res.Button == 1 { /* delete */ }
```

## 4. Menus

`NewMenu(items []*MenuItem) *Menu` · `Separator() *MenuItem` · `(m *Menu) Append(items ...*MenuItem)` ·
`Insert(pos int, item *MenuItem)` · `ItemByID(id string) *MenuItem` · `Items() []*MenuItem` ·
`Remove(ids ...string)` · `Popup(win *Window)` · `PopupAt(win *Window, x, y int)`.
`MenuItem`: `ID`, `Label`, `Type MenuItemType`, `Role MenuRole`, `Accelerator` (`"CmdOrCtrl+Shift+N"`),
`Disabled`, `Hidden`, `Checked`, `KeepOpen`, `ToolTip`, `Icon *MenuIcon`, `Subtitle`, `Badge`,
`BadgeAlign`, `BadgeShape`, `BadgeColor`, `BadgeTextColor`, `Submenu []*MenuItem`,
`Click func(item *MenuItem, win *Window)`. Mutate live items with `SetLabel/SetEnabled/SetVisible/
SetChecked/SetAccelerator/SetSubtitle/SetIcon`.

**`RoleEditMenu` is a whole prebuilt Edit menu** — Undo/Redo/Cut/Copy/Paste/Select All with the platform
accelerators, wired to the focused text field. That one line is the answer to copy/paste shortcuts:

```go
func appMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{     // macOS app menu
			{Role: mygo.RoleAbout, Click: func(*mygo.MenuItem, *mygo.Window) {
				mygo.App.ShowAboutPanel(mygo.AboutPanelOptions{})
			}},
			mygo.Separator(),
			{Role: mygo.RoleQuit},                              // Cmd+Q / Ctrl+Q
		}},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Start download", Accelerator: "CmdOrCtrl+N",
				Click: func(it *mygo.MenuItem, w *mygo.Window) { s.startDownloads(10) }},
			mygo.Separator(), {Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},                              // the whole Edit menu
		{Label: "View", Submenu: []*mygo.MenuItem{
			{ID: "dark", Label: "Dark mode", Type: mygo.MenuItemCheckbox, Checked: true},
			mygo.Separator(), {Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}
```

Install with `mygo.App.SetMenu(m)` **before `Run`**, or per window with `win.SetMenu(m)`. macOS-only roles
are skipped elsewhere, so one template serves all platforms. Individual roles: `RoleUndo`, `RoleRedo`,
`RoleCut`, `RoleCopy`, `RolePaste`, `RolePasteAndMatchStyle`, `RoleDelete`, `RoleSelectAll`, `RoleQuit`,
`RoleAbout`, `RoleClose`, `RoleMinimize`, `RoleToggleFullScreen`, `RoleToggleDevTools` (native UI →
inspector), `RoleZoomIn/ZoomOut/ResetZoom`, `RoleOpenRecent`, `RoleServices`, `RoleHelp`; whole menus
`RoleAppMenu/RoleFileMenu/RoleEditMenu/RoleViewMenu/RoleWindowMenu/RoleFormatMenu`. Edit items enable
only when the focus supports them; for `HandleInput` elements all of them do. `Click` runs on the main
thread; `win` may be nil.

## 5. Storage, config, shell

`func (a *Application) Path(name PathName) (string, error)` (`paths.go:53`) ·
`func (a *Application) SetPath(name PathName, path string)`.

`PathUserData` = `~/Library/Application Support/<Name>`, `~/.config/<Name>` or `%AppData%\<Name>`,
**created on first use** · `PathCache` = the matching cache dir (`%LocalAppData%\<Name>` on Windows) ·
`PathLogs` = where to write logs · `PathAppData` = per-user config dir **without** the app name ·
`PathResources` = shipped files (`Contents/Resources`; the exe's dir elsewhere; under `go run`/`go test`
the working dir's `resources/`) · `PathHome`, `PathTemp`, `PathExe` · `PathDesktop`, `PathDocuments`,
`PathDownloads`, `PathMusic`, `PathPictures`, `PathVideos`.

There is **no separate config dir** — settings live in `PathUserData`, caches in `PathCache`. `App.Path`
works **before `Run`**, so read settings before creating the window. `mygo.config.ts` +
`defineConfig({...})` covers **build/packaging** only (`urlSchemes`, `fileAssociations`, `resources`,
id/name/version) and is consumed by `mygo build`; it is not a runtime settings store. `App.SetName`
decides the data dir and the single-instance lock, so set it first. Clean JSON file, written atomically:

```go
type Settings struct {
	WindowStateKey string `json:"windowStateKey"`
	LastFolder     string `json:"lastFolder"`
	Concurrency    int    `json:"concurrency"`
}

func (s *Shell) settingsPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)     // created on first use
	if err != nil { return "", err }
	if err := os.MkdirAll(dir, 0o755); err != nil { return "", err }
	return filepath.Join(dir, "settings.json"), nil
}

func (s *Shell) saveSettings() error {
	path, err := s.settingsPath()
	if err != nil { return err }
	s.mu.Lock(); b, err := json.MarshalIndent(s.settings, "", "  "); s.mu.Unlock()
	if err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(path), "settings-*.json")  // same dir => atomic rename
	if err != nil { return err }
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil { tmp.Close(); os.Remove(name); return err }
	if err := tmp.Close(); err != nil { os.Remove(name); return err }
	return os.Rename(name, path)
}
```

Load tolerantly — a missing file is normal on first run (`os.ReadFile` error ⇒ defaults).

**Shell** (`var Shell ShellModule`): `OpenExternal(url string) error` · `OpenPath(path string) error` ·
`ShowItemInFolder(path string)` (**no return value**) · `FileIcon(path string, size int) ([]byte, error)`
(PNG bytes) · `TypeIcon(ext string, size int) ([]byte, error)` · `TrashItem(path string) error` ·
`TrashItemResult(path string) (string, error)` · `PreviewItem(path string) error` · `Beep()` ·
`InstalledApps() ([]InstalledApp, error)` · `DefaultApp(url string) (InstalledApp, error)` ·
`LaunchApp(app InstalledApp) error`.

## 6. Notifications

`NewNotification(opts NotificationOptions) *Notification` · `(n) Show() error` ·
`ShowContext(ctx context.Context) error` · `Close()` · `ID() string` · `OnClick(fn) (off)`.
`NotificationOptions`: `ID`, `Title`, `Subtitle`, `Body`, `Silent bool`, `Group string`,
`Image []byte` (encoded PNG/JPEG), `Sender *NotificationSender`.

`NewNotification` only builds it; **`Show()` talks to the OS and is the only call that can fail** (e.g.
permission denied). Reusing an `ID` **replaces** a notification still on screen — ideal for "42% → 88%".
`App.OnNotificationClick(fn func(id string))` receives the ID, even after a relaunch.

```go
n := mygo.NewNotification(mygo.NotificationOptions{
	ID: "download-done", Title: "Download finished", Body: "10 files", Group: "downloads",
})
n.OnClick(func() { if w := currentWindow(); w != nil { w.Show(); w.Focus() } })
if err := n.Show(); err != nil { log.Println(err) }
```

## 7. Headless PNG recipe — **VERIFIED WORKING**

**`ui.Render` renders a `ui.View` to an `*image.RGBA` with no window, no GPU, no `App.Run`.**

```go
func Render(view func(c *Context), width int, height int, scale float32) *image.RGBA  // api_gen.go:2129
```

`width`/`height` are **DIPs**; output pixels = DIPs × `scale`. It wraps the software renderer
(`coreRender`, `ui/headless.go:258`), so **you never import `internal/raster`**.

```go
package main

import (
	"image/png"
	"os"

	"github.com/egoist/mygo/ui"
)

// An ordinary ui view — the same function you'd pass as WindowOptions{Content: ui.View(view)}.
func view(c *ui.Context) {
	ui.Column(c).Padding(20).Gap(10).Children(func() {
		ui.Text(c, "Downloading assets")
		ui.Progress(c, 0.3).Label("Downloading")
		ui.Text(c, "3 of 10 files")
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "Choose folder")
			ui.PrimaryButton(c, "Start download")
		})
	})
}

func main() {
	const w, h, scale = 420, 220, 2                  // DIPs, DIPs, device px per DIP
	img := ui.Render(view, w, h, scale)               // <- headless, one call
	f, err := os.Create("snap.png")
	if err != nil { panic(err) }
	defer f.Close()
	if err := png.Encode(f, img); err != nil { panic(err) }
}
```

The tool in the scratch module wraps this with `-out/-w/-h/-scale/-dark` flags (`cmd/pngsnap`); for
dark mode `ui.Render` takes no appearance argument, so it uses a `Tester` (see below).

**Exact commands that produced a verified PNG:**

```powershell
cd C:\Users\Administrator\project\manager\_probe\scratch-shell
go run ./cmd/pngsnap -out out/snap-standalone.png
#   wrote out/snap-standalone.png: 840x440 px, 21437 bytes
go run ./cmd/pngsnap -out out/snap-dark.png -dark
#   wrote out/snap-dark.png: 840x440 px, 21578 bytes
```

**It worked.** Real files on disk: 840×440 px (420×220 DIPs at `scale 2`), 21 437 / 21 578 bytes. I
opened both — light: white background, blue-filled progress bar, "Downloading assets", "3 of 10 files",
two buttons; dark: the same on near-black, so `-dark` took effect. **Rendering is deterministic**: `out/headless.png` (from `go test`) and `out/snap-standalone.png`
(from the binary) are byte-identical, SHA256 `4760D8F2…f8db657`. Tests also assert the PNG decodes,
has the expected pixel size, and ≥5 distinct colours (not blank). Artifacts: `out/headless.png`
(21 437 B), `out/after-click.png` (6 533 B), `out/snap-standalone.png`, `out/snap-dark.png`.

Crisper text → raise `scale`. Dark mode needs the Tester (`ui.Render` takes no appearance argument).
Other desktop settings: `tt.SetPreferences(ui.Preferences{ReduceMotion: true})`,
`tt.SetDirection(ui.RightToLeft)`, `tt.SetLocale("zh-Hans")`, `tt.SetSize`, `tt.SetZoom`,
`tt.SetWindowActive(false)`; animation stills via `tt.Advance(150 * time.Millisecond)` (the Tester
clock is frozen otherwise) then `tt.Image()`.

Which renderer: `ui.Render(view, w, h, scale) *image.RGBA` is the one to use offline.
`ui.NewTester(...)` + `tt.Image()` gives the same pixels after simulated input (`tt.Frame()` to redraw).
`ui.NewRecorder(view, RecorderOptions{Width, Height, Scale, Dark, Preferences, Locale, Direction,
FrameRate})` gives `r.Frame(time.Duration) *scene.Scene` — a **display list, not pixels**, for video,
needing its own rasterizer. `Window.CapturePage() []byte` is a PNG but needs a **real** window.

### Can another module import `internal/raster`?

**Not from a normally-named module.** Verified: with the scratch module at path `scratchshell`,
`import _ "github.com/egoist/mygo/internal/raster"` fails with `use of internal package … not allowed`.
The `internal` rule keys on the **import path**, so `replace` alone never helps. Two things do:

1. **Don't** — `ui.Render` already wraps it, which is why the recipe above needs no internal import.
2. **Name your module inside the prefix**: a path of `github.com/egoist/mygo/<x>` satisfies the rule.
   Verified — module `github.com/egoist/mygo/probein` with the same `replace` compiled cleanly importing
   both `internal/raster` and `internal/scene` (`go build ./...` exit 0).

If you take route 2: `raster.Render(dst *Image, s *scene.Scene)` · `NewImage(w, h int) *Image` ·
`type Image struct { W, H, Stride int; Pix []byte }` (premultiplied BGRA) · `(m *Image) RGBA() []byte`
(for `image.RGBA{Pix: m.RGBA(), Stride: 4*m.W}`) · `type Renderer struct { Image Image }` with
`Render(s *scene.Scene) []image.Rectangle`, `Invalidate()`, `Release()`. i.e. `raster.Render(img,
rec.Frame(0))`, then wrap `img.RGBA()` — exactly what `ui.Render` does for you.

## 8. Testing views with `ui.Tester`

`NewTester(view func(c *Context), width, height int) *Tester` (`api_gen.go:1836`) ·
`NewTesterWith(view func(c *Context), width, height int, o TesterOptions) *Tester` (`api_gen.go:1844`).
`type TesterOptions struct { Preferences Preferences; Dark bool }` — applied from the **first** frame.
Renders in memory, needs no window, **clock frozen** unless you call `Advance`, so tests are
deterministic without sleeping.

```go
func TestTesterButtonByLabel(t *testing.T) {
	app := &counter{}
	tt := ui.NewTester(app.view, 320, 240)

	box, ok := tt.Find("Increment")                // by visible text, else by Label
	if !ok { t.Fatalf("no button labeled %q; texts=%q", "Increment", tt.Texts()) }
	if box.W <= 0 || box.H <= 0 { t.Fatalf("empty box: %+v", box) }

	if err := tt.Click("Increment"); err != nil { t.Fatal(err) }
	if app.n != 1 { t.Fatalf("count = %d, want 1", app.n) }
	if !tt.HasText("count: 1") { t.Fatalf("texts = %q", tt.Texts()) }
}

var query string
view := func(c *ui.Context) {
	ui.TextInput(c, &query).Label("Search").Placeholder("Search files")
	ui.Text(c, "results: "+query)
}
tt := ui.NewTester(view, 400, 120)
if err := tt.Click("Search"); err != nil { t.Fatal(err) }  // a TextInput is found by its Label
tt.Type("hello")                                           // query == "hello"
```

**API map.** Frames: `Frame()`, `Image() *image.RGBA`, `FramePending() bool`, `Advance(time.Duration)`,
`Now() time.Time`. Inspect: `Find(s) (Rect, bool)`, `Texts() []string`, `HasText(s) bool`,
`Focused(s) bool`, `Cursor()`, `Clipboard()`, `OpenedURLs()`, `TextCaret()`, `Alerts()`,
`Announcements()`. Pointer: `Click(s) error`, `ClickAt(x, y)`, `ClickWith(ui.Shift, s) error`,
`ClickAtWith(mods, x, y)`, `RightClick(s) error`, `Press/Release(x, y)`, `PressButton/ReleaseButton`,
`Move`, `Scroll`, `Wheel`, `CancelPress()`, `DropFiles(x, y, paths...) bool`,
`UsePointer(ui.PointerPen, 0.6)`. Keyboard: `Type(s)`, `TypeKey(mods, key, text)`,
`Key(ui.Cmd, ui.KeyS)`, `KeyDown/KeyUp`, `KeyRepeat`, `HoldModifiers`, `Command("copy")`,
`Compose(text, caret)`. Menus: `RightClick(s)`, `Menu() []string`,
`ChooseMenuItem("Move to", "Archive") error`, `CloseMenu()`. Window/desktop: `SetSize`, `SetScale`,
`SetZoom`, `SetDark`, `SetPreferences`, `SetLocale`, `SetRegion`, `SetDirection`, `SetFocused`,
`SetWindowActive`, `SetWindowHidden`, `SetVibrancy`, `SetTitleBar`, `SetClipboard`, `FailOpenURL`,
`FailClipboard`. Accessibility: `Access(name)`, `AccessByRole(ui.RoleButton, name)`, `AccessAll()`,
`UseScreenReader()`.

**Gotchas.** **Find/Click match rendered text or `Label` — there is no key-based lookup**; `labelNode` is
`{id, text, r}` (no key field) and `Element.Key()` keys *state*, not lookup — give an icon-only button
`.Label("Delete")` so `tt.Click("Delete")` works. **Duplicate keys panic the Tester** where the second
was given (apps merely log it). `Click` **verifies the target first**: error if the element has no
visible area or its centre would hit another control; it does **not** auto-scroll, while `ClickAt`
clicks coordinates blindly. `HasText` is a **substring** match, `Find` is **exact**. After mutating state
outside a frame, call `tt.Frame()` before reading `Image()`. Don't `time.Sleep` — `Advance` the clock.

## Scratch module setup (as verified)

```powershell
cd C:\Users\Administrator\project\manager\_probe\scratch-shell
go mod init scratchshell
go mod edit -replace "github.com/egoist/mygo=C:\Users\Administrator\project\manager\_probe\mygo\github.com\egoist\mygo@v0.9.2"
go mod edit "-require=github.com/egoist/mygo@v0.9.2"   # QUOTES MATTER
go mod tidy                                            # run AFTER .go files exist
```

Two traps: (1) unquoted `-require` fails in PowerShell (`go: -require=github: need path@version`);
(2) `go mod tidy` **before** any `.go` file exists silently drops the require, giving
`module … is replaced but not required` at build time. Write code first, then tidy.
Files: `shell.go` (§1–§6, compile-proof), `tester_test.go` (§7–§8, 4 tests), `doc_snippets_test.go`
(every snippet here, compiled), `cmd/pngsnap/main.go`, `out/*.png`.
