# MyGo v0.9.2 — Layout, Containers, Styling, Theming, Scrolling, Window Chrome

Compile-verified against `github.com/egoist/mygo@v0.9.2` (source read, then `go build ./... && go vet ./...`
clean in `_probe/scratch-layout`). Docs-vs-source conflicts resolved toward the source and flagged in §10.

**Toolchain (verified):** the module needs `go >= 1.27.3` and that floor comes from the *dependency*, not
your main module, so `GOTOOLCHAIN=auto` will not fetch it — set `$env:GOTOOLCHAIN='go1.27.3'`.

## 1. View / state model

```go
func View(view func(c *Context)) *Content        // ui.View → mygo.WindowOptions.Content
mygo.NewWindow(mygo.WindowOptions{Title: "T", Content: ui.View(app.view)})
```
A view is `func(c *ui.Context)`, run on the main thread for every frame that needs one (input,
`Invalidate`, running animations). One stable `*ui.Context` per window; `.Children(func())` swaps the parent.
One `Content` can serve several windows, each with its own state.

**Never store build values.** `ui.Element` is a 16-byte checked handle that *expires before the next build
pass, even in the same frame*. App state lives in your struct; persistent control identity in `ui.Handle`;
per-element frame state in `ui.Local`; services in `ui.Services`.

`*ui.Context` (verified `go doc`):
```go
c.Theme() *Theme   c.SetTheme(*Theme) /*whole window, incl. root*/   c.Root() Element
c.Resolve(Handle) Element   c.Size() (w,h float32)   c.Zoom()/c.SetZoom(z)
c.Preferences() Preferences   c.Key(key any) *Context   c.Valid()   c.Direction()   c.Locale()
c.Invalidate()   c.After(d)   c.AnimationFrame()   c.DisableAnimations()   c.Instant()   c.Now()
c.Shortcut(mods,key) bool   c.OnShortcut(…)   c.OnWindowShortcut(…)
c.TitleBar() TitleBar   c.Vibrancy()   c.WindowActive()   c.WindowHidden()
c.Focused(Handle)   c.FocusWithin(Handle)   c.Services()   c.Router()   c.TextSystem()
c.MeasureText(width float32, spans ...Span) (w,h float32)
c.Toast(msg)   c.AddToast(Toast)   c.CloseToast(id)   c.Announce(text)   c.OpenURL(url)
c.ReadClipboard()   c.WriteClipboard(s)
```
**No `Context.RequestFocus`** — focus lives on element/handle: `e.Focus(FocusOptions...)`, `e.AutoFocus()`,
`h.RequestFocus()`, `h.Focus()/CancelFocus()`, `c.Focused(h)`.

Forcing a repaint — in the view `c.Invalidate()` (another frame after this one) or `c.After(d)` later;
while animating `c.AnimationFrame()` (in `Draw`: `p.After(d)`/`p.AnimationFrame()`); from any goroutine
`win.Update(func(){…})` runs the function on the main thread **and** repaints, `win.Invalidate()` only
repaints, and `mygo.RunOnMain(fn)` runs a compare-and-invalidate without drawing:
```go
go func() {
	items, err := fetchItems()
	a.win.Update(func() { a.items, a.err = items, err })   // state written on the main thread
}()
mygo.RunOnMain(func() { if n != a.count { a.count = n; a.win.Invalidate() } })
```
Reading a MyGo-animated value (`AnimateTo`, `AnimatePose`, `InView`, `ScrollState`) rebuilds the view every animating frame; `AnimatePose` and `Draw`-computed animation only repaint.

## 2. Layout primitives

Flexbox (CSS semantics), units are DIPs. **No `ui.Stack`/`ZStack`, no `ui.Card`, no `ui.Overlay`** —
layering is `Box` + `Absolute` + `ZIndex`.

```go
ui.Column(c)   // stacks top→bottom, stretches to its width
ui.Row(c)      // left→right, children vertically centered
ui.Box(c)      // "lays its children out in a column" — Box == Column
ui.Grid(c)     ui.Scroll(c)   ui.ScrollHorizontal(c)   ui.ScrollBoth(c)
ui.Spacer(c)   ui.Divider(c)  ui.Themed(c, *Theme)
```
Constructors take **only** `c`; children come from `.Children(fn func()) Element`:
```go
ui.Column(c).Fill().Padding(16).Gap(8).Children(func() {
	ui.Text(c, "Documents").Grow(1)
	ui.Text(c, "12 items").TextColor(c.Theme().TextMuted)
})
```
**Sizing** — `Width/Height(f)`, `Size(w,h)`, `AspectRatio(r)`, `WidthPercent/HeightPercent(p)`,
`MinWidth/MaxWidth/MinHeight/MaxHeight(f)`, `Min/MaxWidthPercent(p)` `Min/MaxHeightPercent(p)`,
`Fill() FillWidth() FillHeight()`, `Grow(f) Shrink(f) Basis(v) BasisPercent(p) FillEqually()`.
`Grow(1)` = `flex:1`. **No `Weight`/`Flex`/`Sizes`; `Fill()` takes no arguments.** Min/max pairs combine
as CSS: `MaxWidthPercent(78).MaxWidth(544)`.

**Spacing** — `Gap(f) GapX(f) GapY(f)`; `Padding(v ...float32)` / `Margin(v ...float32)` (1, 2 =
vertical/horizontal, or 4 values, CSS order); `PaddingX/Y MarginX/Y`, `PaddingStart/End MarginStart/End`.
`ui.Auto` is `var Auto = float32(math.Inf(1))` — a **var**, not a const: `Margin(0, ui.Auto)` centers,
`Margin(0,0,0,ui.Auto)` pushes to the end, `MarginStart(ui.Auto)` is RTL-safe.

**Alignment** — `Justify(Align)` (Start Center End SpaceBetween SpaceAround SpaceEvenly),
`AlignItems(Align)` (Start Center End Stretch Baseline), `AlignSelf`, `AlignContent`, `Center()`,
`Wrap() WrapReverse() NoWrap() Reverse()`.

**Positioning** — `Absolute()`, `Top/Right/Bottom/Left(f)`, `Top/Right/Bottom/LeftPercent(p)`,
`Start/End(f)`, `StartPercent/EndPercent(p)`, `ZIndex(n)`, `Sticky().Top(0)`, `Attach(at, self Anchor)`,
`AttachTo(target, at, self)`, `AttachEdgeMargin(v)`, `PassThrough()`. Anchors: `AnchorTopLeft/Top/TopRight/
Left/Center/Right/BottomLeft/Bottom/BottomRight`, `AnchorTopStart/TopEnd`.
```go
ui.Box(c).Size(40, 40).Children(func() {
	ui.Icon(c, bell).FontSize(24)
	ui.Badge(c, "3").Absolute().Top(-4).Right(-6)
})
ui.Box(c).Fill().Children(func() {
	ui.PrimaryButton(c, "New").Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(16).Bottom(16)
})
```
**Clip/visibility** — `Clip() ClipX() ClipY() ClipInset(top,right,bottom,left,radius)` `ClipInsetPercent(…)`
`Debug()`; `Invisible()` (keeps space, no input); `Hidden(bool)` (collapses space, keeps inner scroll/text/
animation state).

**Grid** — `Columns(n)`, `ColumnTracks(...Track)`, `RowTracks(...Track)`, `GridRows(n)`, `GapX/GapY`,
`ColumnSpan(n) RowSpan(n) ColumnStart(n) RowStart(n)` (1-based; negative span = to last track),
`JustifyItems/AlignItems/JustifySelf/AlignSelf`; tracks `ui.Fixed(v)`, `ui.Fr(f)`, `ui.FitContent()`,
`.Min(v)`, `.MinContent()`.
```go
ui.Grid(c).Columns(3).Gap(12).Children(func(){ ui.Image(c, p).AspectRatio(1).Radius(8) })
ui.Grid(c).ColumnTracks(ui.Fixed(220), ui.Fr(1)).GapX(16).Children(func(){ … })
ui.Grid(c).ColumnTracks(ui.Fr(1).Min(240), ui.Fixed(320))      // minmax(240px,1fr)
```
`Fr` shares space even when content is wider (`minmax(0,1fr)`); `.Min` = `minmax(240px,1fr)`. Grid builds
**all** children — for many items use `ui.GridView(c, *GridState, n, minWidth, height, row)`.

## 3. Styling

State, not selectors: `if row.Hovered() { row.Background(t.SurfaceHover) }`.
```go
Background(Color)   Gradient(from, to Color, angle float32)
Border(width float32, c Color)  BorderWidth(v ...float32)  BorderColor(Color)
BorderStyle(ui.BorderSolid | ui.BorderDashed)   // only these two exist
BorderStartWidth(f) BorderEndWidth(f)
Radius(r ...float32)                            // NOT "BorderRadius"
Shadow(x, y, blur, spread float32, c Color)  InsetShadow(same)  NoShadow()
Opacity(o)  Blur(sigma)  Cache()  Material(m Material)  Cursor(c Cursor)  Debug()

ui.Column(c).Padding(18).Gap(12).Radius(10).Background(t.Background).Border(1, t.Border).
	Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.06))
```
**Colors** — `ui.RGB(r,g,b uint8)`, `ui.RGBA(r,g,b uint8, alpha float32)`,
`ui.Hex("#rgb"|"#rgba"|"#rrggbb"|"#rrggbbaa")` (panics on bad input), `ui.Oklch(l,c,h)`, `ui.Transparent`
(a **var**), `ui.Color{R,G,B,A uint8}`; methods `Alpha(a)`, `Mix(o,t)`, `MixOklab(o,t)`, `Over(base)`.

**Gradients/masks**
```go
ui.LinearGradient{From, To Color; Angle float32 /*deg clockwise from up*/; Start, End float32; Oklab bool; Stops []GradientStop}
ui.RadialGradient{Stops []GradientStop; Center ui.Point /*fractions*/; Radius float32; Oklab bool}
ui.ConicGradient{Stops; Center ui.Point; Angle float32; Oklab bool}
ui.GradientStop{Color ui.Color; At float32}      // key the fields — go vet rejects unkeyed
e.LinearGradient(g) e.RadialGradient(g) e.ConicGradient(g)  e.BorderLinearGradient(g) e.BorderConicGradient(g)
e.Stripes(c, width, gap, angle)  e.MaskLinearGradient(g) e.MaskRadialGradient(g) e.MaskConicGradient(g)
e.Masks(ui.Mask{Image: g, Box: ui.MaskContentBox, Composite: ui.MaskExclude}, …)
// Box: MaskBorderBox|MaskPaddingBox|MaskContentBox  Composite: MaskAdd|MaskSubtract|MaskIntersect|MaskExclude

black := ui.RGB(0, 0, 0)
fade := []ui.GradientStop{{Color: ui.Transparent, At: 0}, {Color: black, At: 0.08},
	{Color: black, At: 0.92}, {Color: ui.Transparent, At: 1}}
list.MaskLinearGradient(ui.LinearGradient{Angle: 180, Stops: fade})   // rows fade at the edges
```
**Hover/active/cursor** — `Hovered()`, `Pressed()`, `FocusVisible()`, `Clicked()`, `RightClicked()`,
`DoubleClicked()`, `Dragging()`; `e.Cursor(ui.CursorPointer)` and family (`CursorText/Move/Grab/Grabbing/
Crosshair/NotAllowed/None/Copy/Alias/ContextMenu/Help/Progress/Wait/Cell/ZoomIn/ZoomOut/AllScroll/NoDrop/
VerticalText/ResizeEW/ResizeNS/ResizeE/W/N/S/Default`).

## 4. Theming

`ui.Theme`, the **complete** field list (verified):
```go
Dark bool;  DisableAutoDimming bool;  Background Color
Surface, SurfaceHover, SurfacePressed Color;  Border Color;  Text, TextMuted Color
Accent, AccentHover, AccentPressed, AccentText Color;  InactiveAccent Color
Danger, Warning, Success Color;  Selection Color
Focus Color;  FocusRingWidth, FocusRingOffset float32;  Inverse, InverseText Color
Scrollbar Color;  ScrollbarWidth float32;  ScrollbarStyle ScrollbarStyle
Radius float32;  Spacing float32 /*widget unit: 4 default, 3 compact, 5 roomy*/
FontSize float32;  Font string /*"" = system*/;  TooltipDelay time.Duration
func LightTheme() *Theme;  func DarkTheme() *Theme
func (t *Theme) WithAccent(Color) *Theme;  func (t *Theme) WithPreferences(Preferences) *Theme
func (t *Theme) Space(n float32) float32;  func (t *Theme) Rem(n float32) float32
```
Mutate a **copy**, then `c.SetTheme`. `c.Theme()` already follows light/dark and the desktop:
```go
t := *c.Theme().WithAccent(ui.Hex("#7c3aed")).WithPreferences(c.Preferences())
t.Radius, t.Spacing = 8, 4
c.SetTheme(&t)

t2 := *ui.DarkTheme()                                    // a hand-built dark palette
t2.Background, t2.Surface, t2.SurfaceHover = ui.Hex("#18181b"), ui.Hex("#27272a"), ui.Hex("#3f3f46")
t2.Border, t2.Text, t2.TextMuted = ui.Hex("#3f3f46"), ui.Hex("#fafafa"), ui.Hex("#a1a1aa")
t2.Accent, t2.AccentText = ui.Hex("#7c3aed"), ui.Hex("#ffffff")
t2.Inverse, t2.InverseText = ui.Hex("#3f3f46"), ui.Hex("#fafafa")   // tooltips/toasts
```
`WithPreferences` **undoes its own earlier following first** (text is never scaled twice), so re-applying it
every frame to `c.Theme()` is safe; a hand-built theme does *not* follow the desktop unless you pass it.
Local scope: `ui.Themed(c, t)` — children (and overlays opened inside) see `t`; `nil` gives a plain column.

**OS appearance** — `mygo.Theme.SetSource(mygo.ThemeSystem|ThemeLight|ThemeDark)`, `.Source()`, `.IsDark()`, `.Preferences()`, `off := mygo.Theme.OnUpdated(fn)`.

**Text** — `FontSize(f) Font(family) LineHeight(m) FixedLineHeight(f) Bold() FontWeight(n) Italic()
LetterSpacing(f) Underline() Strikethrough() TextColor(c) TextAlign(Align) MaxLines(n) Ellipsis(s)`.

**Preferences** — `p.Accent (Color)`, `p.HighContrast`, `p.TextScale float32`, `p.ReduceMotion`, `p.ReduceTransparency`, `p.Pointer/p.AnyPointer` (`ui.PointerFine|PointerCoarse|PointerNone`), `p.NoHover/p.NoAnyHover`, `p.Hover()/AnyHover()`.

## 5. Scrolling

`ui.Scroll(c)` / `ui.ScrollHorizontal(c)` / `ui.ScrollBoth(c)` — a container **needs a size**
(`Grow(1)`, `Fill()`, or an explicit height); it does not shrink-wrap.
```go
ui.Scroll(c).Grow(1).Padding(16).Gap(12).Children(func(){ for _, p := range a.posts { post(c, p) } })
```
**Position control**
```go
ui.Scroll(c).TrackScroll(&a.scroll)          // *ui.ScrollState kept in app state
type ScrollState struct{ Handle; X, Y, MaxX, MaxY float32 }
s.ScrollTo(x, y float32, a Animation)        // 0 = start, math.MaxFloat32 = end; s.Scrolling() bool
if a.log.Y >= a.log.MaxY { a.log.Y = math.MaxFloat32 }   // follow the end unless the user scrolled up
a.feed.ScrollTo(0, 0, ui.Smooth)
```
Per element: `e.ScrollIntoView(opts ...ScrollIntoViewOptions)` — call only in the frame it appears;
`Block`/`Inline` = `ui.ScrollNearest` (default) | `ScrollStart` | `ScrollCenter` | `ScrollEnd`, plus
`Animation: &ui.Smooth`.

**Sticky** — `e.Sticky().Top(0)` / `.Start(0)`; each axis follows the nearest scroller on that axis (CSS
cannot). Give it an opaque background; it paints above siblings. In `List`, use `ListState.Header` instead.

**Scrollbar knobs** — `e.Scrollbars(bool)`, `e.ScrollbarAxes(h, v bool)`, `e.KeepScrollbars(true)`,
`e.ScrollbarStyle(ui.ScrollbarStyle{…})`, `e.ScrollbarInsets(64, 0, 0)` (CSS-Padding order; clears a
floating toolbar). `ScrollbarStyle` zero fields follow the theme: `TrackWidth` 0 → `ThumbWidth+6`,
`ThumbWidth` 0 → theme `ScrollbarWidth` (6), `Radius` 0 → half width unless `Square`, `MinLength` 0 → 24;
colors `Track`, `Thumb`, `Hover`, `Active`.

**Snap** — `e.ScrollSnap(ui.SnapMandatory|SnapProximity)`, `e.SnapAlign(ui.Start|Center|End)`, `SnapAlignX/Y`.

**Virtualized rows** — `ui.List` builds only rows in view; `ListState` is the knob set:
```go
var s ui.ListState
s.Key func(row int) any   s.KeepOffset   s.FollowEnd   s.FollowAnimation *Animation
s.MoveAnimation *Animation   s.Selected *int   s.Selection ui.Selector   s.Header func(row int) bool
s.Label func(row int) string   s.NearEnd int   s.EndReached func(last int)
s.ScrollTo(row int, align ui.Align)   s.ScrollToEnd()   s.ScrollIntoView(row int)   s.AtEnd()
s.Visible() (first, last int)   s.Choose(row int)   s.HoldPlace()
ui.List(c, &s, n, func(i int){ … }).Fill().KeepScrollbars(true)
```
## 6. Containers for an app shell

```go
ui.Sidebar(c, &sel string, fn func())                        // needs a width
ui.SidebarSection(c, title string, open *bool, fn func())    // open nil = always expanded
ui.SidebarItem(c, id string, icon *ui.SVG, label string)     // nil icon OK
ui.Tabs(c, &i int, labels ...string)      ui.TabsBase(c, &i int, n) TabsParts  // .List/.Tab(i)
ui.Segmented(c, &i int, labels ...string)
ui.Split(c, &w float32, first, second func())    ui.SplitVertical(c, &h float32, …)
ui.SplitPercent(c, &p float32, …)                ui.SplitVerticalPercent(c, &p float32, …)
e.SplitReset(220)      // double-click the divider restores; ≥40 DIP kept at each edge
ui.Toolbar(c, fn func())   ui.Divider(c)   e.Dividers(width float32, c Color)

ui.Sidebar(c, &a.page, func() {
	ui.SidebarSection(c, "Mailboxes", &a.open, func() {
		ui.SidebarItem(c, "/inbox", nil, "Inbox").Children(func(){ ui.Badge(c, "12") })
		ui.SidebarItem(c, "/sent", nil, "Sent")
	})
}).Width(220).Label("Mailboxes").Changed()
```
**No `ui.Card`** — compose it (the gallery's own helper):
```go
func card(c *ui.Context, title string, body func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(18).Gap(12).Radius(10).Background(t.Background).
		Border(1, t.Border).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.06)).Children(func() {
		if title != "" { ui.Text(c, title).FontSize(15).Bold() }
		body()
	})
}
```
## 7. Window chrome

```go
Title string   Width, Height int /*800×600*/   UseContentSize   X, Y int
MinWidth, MinHeight, MaxWidth, MaxHeight int
Hidden   Frameless   TitleBarStyle /*TitleBarDefault|TitleBarHidden|TitleBarHiddenInset*/
TitleBarHeight int /*0 → 32 Windows, header-bar height Linux*/   TrafficLightPosition *mygo.Point
HideWindowButtons   DisableResize/DisableMove/DisableMinimize/DisableMaximize/DisableClose/DisableFullScreen
DisableShadow   AlwaysOnTop   FullScreen   Maximized   SkipTaskbar   AutoHideMenuBar
Transparent   BackgroundColor string /*CSS, e.g. light-dark(#f5f5f7,#1e1e1e)*/
Vibrancy   VibrancyTint string   CornerRadius int   Opacity float64   IgnoreMouseEvents   Panel   FitContent
Parent *Window   Modal   StateKey string   Content Content
```
`Vibrancy`: `None|Window|Content|Sidebar|Header|Titlebar|UnderWindow|UnderPage|Menu|Popover|HUD|Sheet|
Tooltip|Selection|FullScreenUI|Glass|GlassClear|Mica|Acrylic|Tabbed`. **No `Material` field** on
`WindowOptions` — that is `Vibrancy` (`ui.Material` is an unrelated *interface* for `Element.Material`).

**Custom title bar in the view** — `bar := c.TitleBar()` is `struct{Height, Left, Right, Bottom float32}`;
`bar.Bottom` is where the controls end (traffic-light bottom on macOS, `== Height` on Linux/Windows):
```go
ui.Row(c).Height(max(bar.Height, 36)).Padding(0, bar.Right+12, 0, bar.Left+12).
	DragWindow().Children(func(){ ui.Text(c, a.router.Title()).Bold() })
// a sidebar reaching the top:  Padding(bar.Bottom+8, 8, 8, 8).DragWindow()
ui.Button(c, "–").OnClick(a.win.Minimize)          // with HideWindowButtons: true, draw your own
ui.Button(c, "□").OnClick(a.win.Maximize)          // a.win.Unmaximize() restores
ui.Button(c, "×").OnClick(a.win.Close)             // ui.Button(c,"⋯").MaximizeButton() = Snap Layouts
```
`DragWindow()` drags and double-click zooms; `e.WindowDoubleClicked()` takes over the double-click.
**Vibrancy** — `if c.Vibrancy() { c.Root().Background(ui.Transparent) }`, else paint it yourself.

**Window methods** — `w.Update(fn)`, `w.Invalidate()`, `w.SetTitle/Title`, `w.Show/Hide/Close/Destroy`,
`w.Center`, `w.Minimize/Maximize/Unmaximize/SetFullScreen/IsMaximized/IsFullScreen`,
`w.SetContentSize/ContentSize/SetSize/SetPosition/SetOpacity`, `w.SetVibrancy(+)`,
`w.SetActiveAppearance(b)`, `w.SetBackgroundColor(s)`.

**Persistence** — `StateKey` alone suffices (JSON in `window-state.json` under `PathUserData`); to own it:
```go
type WindowStateStore interface {
	LoadWindowStates() (map[string]WindowState, error)
	SaveWindowStates(map[string]WindowState) error
}
mygo.App.SetWindowStateStore(store)      // nil → default file
mygo.App.ClearWindowStates() error
mygo.WindowState{X, Y, Width, Height int; Maximized, FullScreen bool; Pixel*}
```
## 8. Motion & transitions

Ask the element for the current value each frame; the key must be stable per element:
```go
x := card.AnimateTo("x", target, ui.Snappy); card.Translate(x, 0)

AnimateTo(key any, target float32, a Animation) float32     Animate(key, target, d)
AnimateWith(key, target, d, ease Easing)                    Spring(key, target, opts ...SpringOptions)
AnimateColor(key any, target Color, a Animation) Color      Jump(key, value) float32
Settled(key)  Arrived(key)  Velocity(key)  Loop(key, period, ease)
Keyframes(key, frames []Keyframe, options ...KeyframeOptions)     Timeline(key, o TimelineOptions) *Timeline

ui.Smooth  ui.Snappy  ui.Bouncy          // spring vars — &ui.Snappy works
ui.Animation{Duration: 150*time.Millisecond, Ease: ui.EaseInOut}
ui.Animation{Spring: ui.SpringOptions{Duration: 700*time.Millisecond, Bounce: 0.1}}
ui.Animation{Spring: ui.SpringOptions{Stiffness: 400, Damping: 30}}
ui.Animation{Duration: 200*time.Millisecond, NonMotion: true}   // fades still run under ReduceMotion
// Animation fields: Duration, Ease, Spring, Delay, Oklab, NonMotion
// SpringOptions: Duration, Bounce, Mass, Stiffness, Damping, Precision, Velocity
ui.EaseIn  ui.EaseOut  ui.EaseInOut  ui.Linear  ui.Bounce(e)  ui.CubicBezier(x1,y1,x2,y2)
```
**Cheapest hover** — draw-only, no view rebuild (`Pose{X, Y, Scale, Rotate, Fade, XPercent, YPercent}`):
```go
lift := ui.Pose{}
if card.Hovered() { lift = ui.Pose{Y: -4, Scale: 1.02} }
card.AnimatePose(lift, ui.Snappy)
```
`AnimatePose` snaps the first pose it sees — for an entrance, set the start pose, `c.Invalidate()`, then set
the target next frame.

**Element transitions** (layout-driven)
```go
ui.ElementTransition{Duration, Ease, Animation *Animation,
	Position, Size, Colors, Shadows bool /*none set = all*/, Width, Height bool,
	Enter, Exit *ui.Motion, AfterExits, ExitDuration, ExitDelay, ExitEase, Match any, Fly, ScaleContent}
ui.Motion{X, Y, XPercent, YPercent, Opacity, Collapse, Scale, Rotate, Blur, Flip}

e.Transition(ui.ElementTransition{Colors: true, Duration: 120*time.Millisecond})   // hover tint
e.Transition(ui.ElementTransition{Height: true, Animation: &ui.Snappy})            // panel spring
e.Transition(ui.ElementTransition{Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}})
e.ChildExit(ui.Motion{X: -40});  e.Transitioning()
```
A negative `Duration` is instant with no exit copy (the app-level ReduceMotion override). Text morphs:
`ContentTransition(ui.ContentTransition{Numeric: true, Down: true})` / `{Morph: true}`.

**Router** — `r := ui.NewRouter("/inbox")` once, in app state; `r.View(c, func(rt *ui.Route){…})` with
`rt.Match("/notes/{id}")`, `rt.Title(…)`, `rt.Param("id")`; `r.Push/Replace/Back/Forward/Go`,
`r.Path/Query(name)/Location/Title/CanGoBack/CanGoForward`; `r.Transition = ui.TransitionMatch`,
`r.Animation = &ui.Snappy`. Pages keep ~10 back/forward of element state (scroll, text, focus).

## 9. Modern app shell + cards (compiles: `_probe/scratch-layout/shell/main.go`)

```go
func (a *app) view(c *ui.Context) {
	t := *c.Theme().WithAccent(ui.Hex("#7c3aed")).WithPreferences(c.Preferences())
	t.Radius, t.Spacing = 8, 4
	c.SetTheme(&t)

	bar := c.TitleBar()
	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	if c.Vibrancy() { root.Background(ui.Transparent) }
	root.Children(func() {
		side := ui.Column(c).Width(240).Shrink(0).Padding(bar.Bottom+8, 8, 8, 8).DragWindow()
		if !c.Vibrancy() { side.Background(t.Surface) }
		side.Children(func() {
			page := a.router.Path()
			if ui.Sidebar(c, &page, func() {
				ui.SidebarSection(c, "Mailboxes", nil, func() {
					ui.SidebarItem(c, "/inbox", nil, "Inbox").Children(func(){ ui.Badge(c, "12") })
					ui.SidebarItem(c, "/sent", nil, "Sent")
				})
			}).Grow(1).Label("Mailboxes").Changed() { a.router.Push(page) }
		})

		ui.Column(c).Grow(1).MinWidth(0).Children(func() {     // MinWidth(0): lets it shrink
			ui.Row(c).Height(max(bar.Height, 36)).Padding(0, bar.Right+12, 0, bar.Left+12).
				DragWindow().Children(func() {                 // custom title bar
				ui.Text(c, a.router.Title()).Bold()
				ui.Spacer(c)
				ui.Segmented(c, &a.tab, "All", "Unread")
			})
			a.router.View(c, func(rt *ui.Route) {
				ui.Scroll(c).Grow(1).Padding(16).Gap(12).Children(func() {
					a.card(c, "Subject", "Preview line")
				})
			})
		})
	})
}

func (a *app) card(c *ui.Context, title, body string) ui.Element {
	t := c.Theme()
	e := ui.Column(c).Padding(14).Gap(6).Radius(t.Radius).
		Background(t.Surface).Border(1, t.Border).
		Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.08)).
		Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	if e.Hovered() { e.Background(t.SurfaceHover) }
	lift := ui.Pose{}
	if e.Hovered() { lift = ui.Pose{Y: -2, Scale: 1.01} }
	e.AnimatePose(lift, ui.Snappy)
	e.Children(func() {
		ui.Text(c, title).Bold()
		ui.Text(c, body).FontSize(13).TextColor(t.TextMuted).MaxLines(2)
	})
	return e
}
```
Checklist: root `Row.Fill().AlignItems(ui.Stretch)` → sidebar `Width/Shrink(0)` → content column
`Grow(1).MinWidth(0)` → title bar → toolbar → `router.View` → `Scroll.Grow(1)` → cards. Split panes:
`ui.Split(c, &a.w, left, right).Fill().SplitReset(320)`.

## 10. Gotchas / docs-vs-source

1. **`ui.Stack`/`ZStack`/`Card`/`Overlay` do not exist** → `Box`+`Absolute`+`ZIndex`, `Column`, `Popover`/`Modal`/`Dialog`.
2. **`BorderRadius` does not exist** → `Radius(v ...float32)`. **`Weight`/`Flex`/`Sizes` do not exist** →
   `Grow`/`Shrink`/`Basis`/`BasisPercent`/`FillEqually`. **`Fill()` takes no arguments.**
3. `ui.Box` **is** a column, not a stacking container.
4. Unkeyed `ui.GradientStop{color, at}` literals (as in the Chinese docs) fail `go vet` — use `{Color:…, At:…}`.
5. `ui.Material` is an **interface** (`Element.Material(m)`); the window material is `WindowOptions.Vibrancy`.
6. `ui.Toast` = `ID/Title/Description/Type/Timeout/Action/OnAction/OnClose/Data` — no `Message`
   (`c.Toast(string)` is the shortcut).
7. `ListState.ScrollTo(row int, align Align)`; `ListState` has **no `Scrolling()`** — that is `ScrollState.Scrolling()`.
8. `Element.Rows(fn func(ListRow))` is a **List** API; grids use `GridRows(n int)`.
9. `KeyframeOptions.Run` and `TimelineOptions.Run` are **`uint64`** counters, not bools.
10. `Router.Query(name string) string` takes an argument.
11. `BorderStyle` has exactly `ui.BorderSolid` and `ui.BorderDashed` — no dotted.
12. `Painter`: `Stroke(r Rect, c Color, radius, width float32)`, `Shadow(r, radius, x, y, blur, spread float32, c Color)`, `FocusRing(r Rect, radius [4]float32)`.
13. **No `Context.RequestFocus`** → `Element.Focus(…)`, `Element.AutoFocus()`, `Handle.RequestFocus()`.
14. Needs `go >= 1.27.3`; `GOTOOLCHAIN=auto` will not fetch it (the floor comes from the dependency).
15. Reading an animated value rebuilds the view every animating frame — prefer `AnimatePose`/`Draw` in hot paths.

## Verification

```sh
cd C:\Users\Administrator\project\manager\_probe\scratch-layout
$env:GOTOOLCHAIN='go1.27.3'
go build ./... && go vet ./...   # both exit 0
gofmt -l .                       # clean
```
`main.go` (layout, styling, theme, scroll, containers, window, motion), `shell/main.go` (§9 shell + cards),
`probe2/main.go` (focus, Painter, masks, cursors, anchors, List/GridView, Local, window-state, preferences).
