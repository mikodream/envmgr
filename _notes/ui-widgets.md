# MyGo `ui` — Interactive Widgets Cheat Sheet

`github.com/egoist/mygo@v0.9.2`. Every signature was read from `ui/api_gen.go` /
`ui/*.go` and compiled in `_probe\scratch-widgets` (`go build ./...` +
`go vet ./...` clean, Go 1.27.4).

## 0. Ground rules

Every constructor takes `*ui.Context` first; there is no global UI state. A view
is a function of your state and **runs every frame**:

```go
mygo.App.WhenReady(func() {
    mygo.NewWindow(mygo.WindowOptions{
        Title: "App", Width: 800, Height: 600, Content: ui.View(app.View),
    })                       // ui.View(func(c *ui.Context)) *Content
})
if err := mygo.App.Run(); err != nil { log.Fatal(err) }
```

`Element` is a 16-byte handle that **expires before the next build pass** — read
its result in the same statement. You own all state: widgets write through the
pointers you pass (`*string`, `*bool`, `*int`, `*ListState`). There is no internal
model to sync. `Disabled`/`ReadOnly`/`Step` set in one build govern the *next*
frame's input.

```go
if ui.Button(c, "Save").Clicked() { app.save() }   // correct
```

## 1. Reading results

| Method | Meaning |
|---|---|
| `Clicked() bool` | primary click, or Enter/Space while focused, since last frame |
| `Changed() bool` | this widget's value changed in **this build pass** |
| `Submitted() bool` | Enter in a single-line input; double-click/Enter in List/Table |
| `Dismissed() bool` | Escape/outside-click closed Modal/Popover and set `*open=false` |
| `RowClicked()` / `RowDoubleClicked()` | click on the **row**, without consuming it |
| `DoubleClicked()` / `RightClicked()` | consumes the click (breaks row selection) |
| `Hovered()` `Pressed()` `Focused()` `FocusVisible()` `FocusWithin()` | pointer & focus |
| `TextSelection() (int,int)` | rune offsets, sorted; equal ⇒ that's the caret |
| `TextAnchorCaret() (int,int)` | anchor + caret (distinguishes reverse selection) |
| `Selected(on bool)` | setter: paint selection state |
| `IsDisabled() bool`, `Bounds() Rect` | state & committed geometry |

Use `Clicked` on buttons; `Changed` on Select/Switch/Segmented/Tabs/Sidebar/List.
Callbacks run after construction, before the rebuild: `OnClick`, `OnChange`,
`OnSubmit`, `OnShortcut(mods, key, fn)`.

## 2. Buttons

```go
func Button(c *Context, label string) Element
func PrimaryButton(c *Context, label string) Element   // accent fill
func ButtonBase(c *Context) Element                   // unthemed
```

```go
if ui.PrimaryButton(c, "Save").Clicked() { app.save() }
ui.PrimaryButton(c, "").Children(func() {             // icon + text
    ui.Icon(c, save); ui.Text(c, "Save").SingleLine()
})
```

`.Disabled(true)` = no clicks, no focus, 50% opacity; `.AccessDisabled(true)`
keeps it reachable by assistive tech. `.Menu(build)` / `.PopoverMenu(build)`
attach a dropdown to any button.

## 3. Text input

```go
func TextInput(c *Context, value *string) Element
func TextArea(c *Context, value *string) Element
func TextInputBase(c *Context, value *string) Element      // no padding/bg/border
func TextAreaBase(c *Context, value *string) Element
func TextInputBuffer(c *Context, value *TextBuffer) Element
func TextAreaBuffer(c *Context, value *TextBuffer) Element
func EditableText(c *Context, value *string) Element       // in-place rename
func SearchField(c *Context, query *string) Element
```

**The app owns the string.** The widget writes into `*value` as the user types;
writing to it yourself replaces the text. Read `app.query` directly (no getter).

| Call | Effect |
|---|---|
| `.Placeholder(s)` | empty-state text (1 line in `TextInput`, wraps in `TextArea`) |
| `.PlaceholderColor(col)` | overrides theme `TextMuted` |
| `.Label(s)` | accessible name — **always set** |
| `.Password()` | masks text **in the frames that call it**; omit the call to reveal |
| `.ReadOnly(true)` | focusable, no caret, text selectable/copyable |
| `.Disabled(true)` | greys out |
| `.MaxLength(n)` | UTF-16 code units; `n<=0` unlimited; app-set values not cut |
| `.Lines(min,max)` | `TextArea` grows with content, then scrolls |
| `.Height(h)` | fixed height, scrolls inside |
| `.CaretWidth(w)` / `.CaretColor(c)` | `0` hides the caret |
| `.Search()` | declares an ARIA searchbox (custom search input) |
| `.Invalid(b)` / `.Error(msg)` | invalid without/with a message |
| `.TextRanges(...)` | per-frame spans: `ui.TextRange{Start,End,Color,Weight}` |
| `.OnPaste(fn func(string) bool)` | `true` takes over the paste |
| `.OnTextKey(fn func(TextKey) bool)` | pre-dispatch keys; `true` consumes |
| `.ReplaceText(start,end,s)` | app edit, one undo step, offsets clamped |
| `.SetTextSelection(start,end)` | move caret / set selection |
| `.TrackLines(*TextLines)` / `.TrackScroll(*ScrollState)` | gutter line numbers |
| `.FontSize/.TextColor/.TextAlign/.LetterSpacing` | typography |

**Password + reveal** — `Password` is a per-frame call, not a flag:

```go
in := ui.TextInput(c, &app.secret).Label("Password").MaxLength(64)
if !app.showSecret { in.Password() }        // reveal = simply don't call it
```

It does nothing to `TextArea` — only single-line fields mask.

**Enter / submit.** `Submitted` is the single-line signal:

```go
if ui.TextInput(c, &app.name).AutoFocus().Submitted() { app.rename() }
```

For a multiline field where Enter sends, use `OnTextKey`:

```go
ui.TextArea(c, &app.draft).Lines(1, 8).OnTextKey(func(k ui.TextKey) bool {
    if k.Key == ui.KeyEnter && k.Mods == 0 {
        app.send(k.Text()); k.SetText(""); return true
    }
    return false                            // Shift+Enter inserts a newline
})
```

**Validation** — `Error` belongs on the `Field`; it also paints the control's
border in `Danger`:

```go
var msg string
if app.email != "" && !strings.Contains(app.email, "@") { msg = "Enter an email address." }
ui.Field(c, "Email", func() {
    ui.TextInput(c, &app.email).Invalid(app.touched && app.email == "")
}).Description("For receipts.").Error(msg)       // empty msg ⇒ valid again
```

**Selection & app edits:**

```go
in := ui.TextArea(c, &app.draft)
start, end := in.TextSelection()            // rune offsets
anchor, caret := in.TextAnchorCaret()
if text, ok := app.generated(); ok {
    in.ReplaceText(0, math.MaxInt, text)    // undo restores what the user wrote
}
```

**Fresh editor** (drops selection/undo/IME state):

```go
ui.TextInput(c.Key(app.session), &app.secret).Password().Label("Secret")
app.session++                               // next build creates a new editor
```

## 4. Select / Combobox — controlled choice

```go
func Select(c *Context, selected *string, options []string) Element
func Combobox(c *Context, selected *string, options []string) Element
func SelectBase[T comparable](c *Context, selected *T) SelectParts[T]
func ComboboxBase(c *Context, text *string) ComboboxParts
```

Both controlled by `*string`; `Changed` reports a new choice.

```go
ui.Select(c, &app.sdk, []string{"SDK 34", "SDK 35", "SDK 36"}).Label("SDK version")
ui.Combobox(c, &app.font, fonts).Label("Font").Placeholder("Pick a font")
```

Select: Enter/Space/Up/Down open, arrows/Home/End move, Enter/Space/click choose,
Escape closes, flips above when there's no room. Combobox: typing filters (prefix
matches first), Down shows all, blur restores the current value. Combobox for long
lists, Select for short ones, Radio when options must stay visible.

## 5. Checkbox / Switch / Radio

```go
func Checkbox(c *Context, checked *bool, label string) Element
func CheckboxState(c *Context, state *CheckState, label string) Element   // tri-state
func CheckboxGroup(c *Context, label string, fn func()) Element
func Switch(c *Context, on *bool) Element
func Toggle(c *Context, on *bool, label string) Element    // switch + inline label
func ToggleGroup(c *Context, fn func()) Element
func Radio[T comparable](c *Context, selected *T, value T, label string) Element
func RadioGroup(c *Context, fn func()) Element
```

```go
if ui.Checkbox(c, &app.telemetry, "Send usage data").Changed() { app.savePrefs() }
if ui.Switch(c, &app.autoUpdate).Label("Auto update").Changed() { app.apply() }
ui.Radio(c, &app.view, 0, "List")
```

`Switch` takes no label argument — use `.Label()`, or wrap it in `ui.Field` (in a
`ui.Form` the label gets its own column). `Element.Mixed()` sets the third state.

## 6. Segmented / Tabs — controlled index

```go
func Segmented(c *Context, selected *int, labels ...string) Element
func Tabs(c *Context, selected *int, labels ...string) Element
```

Same contract: `*int` index, `Changed` on a new index, arrow keys once focused,
one Tab stop (on the selected item).

```go
if ui.Segmented(c, &app.view, "List", "Grid").Label("View").Changed() { app.refresh() }
switch app.view { case 0: app.list(c); case 1: app.grid(c) }

ui.Segmented(c, &app.appearance, "Light", "Dark", "System").FillEqually()

ui.Tabs(c, &app.tab, "General", "Appearance", "Advanced")
switch app.tab { case 0: app.general(c) }
```

`Segmented` = toolbar view switch; `Tabs` = pages in a panel; history/back ⇒
`ui.Router`. Icons in segments ⇒ `ui.SegmentedBase` (`Track` + `Segment`).

## 7. Sidebar

```go
func Sidebar(c *Context, selected *string, fn func()) Element
func SidebarSection(c *Context, title string, open *bool, fn func()) Element
func SidebarItem(c *Context, id string, icon *SVG, label string) Element
```

Selection is the item **ID**. Give it a width; `nil` `open` = always expanded;
`nil` icon is fine.

```go
if ui.Sidebar(c, &app.page, func() {
    ui.SidebarSection(c, "Mailboxes", &app.mailboxesShown, func() {
        ui.SidebarItem(c, "inbox", inboxIcon, "Inbox").Children(func() {
            ui.Badge(c, fmt.Sprint(app.unread))
        })
        ui.SidebarItem(c, "sent", sentIcon, "Sent")
    })
    ui.SidebarSection(c, "Tags", nil, func() { ui.SidebarItem(c, "work", nil, "Work") })
}).Width(220).Changed() {
    app.router.Push(app.page)
}
```

## 8. Virtualized List

```go
func List(c *Context, s *ListState, n int, row ...func(int)) Element
```

Only visible rows (+a few) are built, so a million rows cost like ten. Row
heights are measured when shown, estimated elsewhere. `row` is **variadic on
purpose**: omit it to configure `ItemKey`/`Selection` first, then call `.Rows`.

```go
ui.List(c, nil, len(app.files), func(i int) {        // nil state is legal
    ui.Text(c, app.files[i].Name).Padding(6, 12)
}).Grow(1)
```

**`ListState`** — one per list; set fields *before* building.

| Field | Purpose |
|---|---|
| `Key func(int) any` | row identity: selection/scroll/focus follow the *item*, not the index |
| `Selected *int` | single selection, `-1` = none; `Changed` reports, `Submitted` = double-click/Enter |
| `Selection Selector` | multi-select (`*ui.Selection[K]`): Cmd/Shift-click, Cmd+A |
| `Label func(int) string` | accessible name; enables type-first-letter jumping |
| `Header func(int) bool` | sticky section headers |
| `Reorder func(rows []int, to int)` | drag to reorder |
| `DragRows func(rows []int) any` | drag rows out (excludes `Reorder`) |
| `DragSelect bool` | drag across rows to select (needs `Selection`) |
| `FollowEnd` + `FollowAnimation *Animation` | chat/log: pin & glide to the end |
| `KeepOffset bool` | keep offset (not the top item) as rows move — for re-sorted rows |
| `EndReached func(last int)` + `NearEnd int` | infinite scroll; fires on **user** scroll |
| `MenuKeepsSelection bool` | right-click doesn't change the selection |

Programmatic control:

```go
app.list.ScrollTo(row, ui.Center)      // ui.Start | ui.Center | ui.End
app.list.ScrollIntoView(row)
app.list.ScrollToEnd()                 // works for rows not yet built
app.list.Choose(row); app.list.ExtendTo(row)
app.list.HoldPlace()                   // stay put for one layout (a fold opened)
first, last := app.list.Visible()      // last frame's viewport
if !app.list.AtEnd() { /* "jump to latest" */ }
```

**Row builder (a)** `func(i int)` — read clicks on a child so the row still
handles selection:

```go
ui.List(c, &app.list, len(app.files), func(i int) {
    if ui.Text(c, app.files[i].Name).Padding(6, 12).FillWidth().RowClicked() {
        app.open(i)
    }
}).Grow(1)
```

**Row builder (b)** `.Rows(func(row ui.ListRow))` — full row state; use when you
draw your own hover/press. `ListRow` = `Index`, `Context`, `Selected()`,
`Hovered()`, `Pressed()`, `ListFocused()`, `PointerPosition()`:

```go
ui.List(c, &app.chats, len(app.items)).Grow(1).Rows(func(r ui.ListRow) {
    bg := ui.Transparent
    switch {
    case r.Pressed(): bg = r.Context.Theme().SurfacePressed
    case r.Hovered(): bg = r.Context.Theme().SurfaceHover
    }
    if r.Selected() && r.ListFocused() { bg = r.Context.Theme().Accent }
    ui.Box(r.Context).Background(bg).Padding(8, 12).Children(func() {
        ui.Text(r.Context, app.items[r.Index].Title)
    })
})
```

> Query `ListRow` for hover/press, **not** a child element — a child answering
> `Pressed`/`Hovered` swallows the press and breaks row selection.

**Empty state** — build siblings inside the list; they show when there are no rows:

```go
ui.List(c, nil, len(results), rowFn).Grow(1).Children(func() {
    if len(results) == 0 {
        ui.Text(c, "No results").TextColor(c.Theme().TextMuted).Padding(12)
    }
})
```

**Multi-select:** `app.list.Selection = &app.chosen` (`ui.Selection[string]`),
then `Len()`, `Has(p)`, `Add(p)`, `Remove(p)`, `Clear()`, `for p := range .All()`.
Replacing the row set? `app.list = ui.ListState{}` forgets selection & scroll.

## 9. Table

```go
func Table(c *Context, s *ListState, columns []TableColumn, n int,
           cell func(row int, col int)) Element

type TableColumn struct {
    Title, ID string                    // ID defaults to Title; used by sort + layout
    Width, MinWidth, MaxWidth float32   // Width 0 = share remaining space
    Align     Align                     // ui.Start | ui.Center | ui.End
    Sortable  bool
    Fixed     bool                      // not movable/resizable
    RowHeader bool                      // spreadsheet row-number column
}
```

Same virtualization and the **same `ListState`** as `List`.

```go
cols := []ui.TableColumn{
    {Title: "Name", Sortable: true, MinWidth: 120},
    {Title: "Size", Width: 90, Align: ui.End, Sortable: true},
}
app.files.Selected = &app.file
app.files.Sort = &app.sort
sorted := sortFiles(app.files, app.sort)          // YOU sort; table shows given order
app.files.Key = func(i int) any { return sorted[i].ID }   // selection follows the item

tb := ui.Table(c, &app.files, cols, len(sorted), func(row, col int) {
    switch col {
    case 0: ui.Text(c, sorted[row].Name).SingleLine()
    case 1: ui.Text(c, sorted[row].Size())
    }
}).Grow(1)
if tb.Submitted() { app.open(sorted[app.file]) }  // double-click or Enter
```

- **Sorting is yours.** A `Sortable` header click updates `ListState.Sort`
  (`SortOrder{Column string, Descending bool}`); you reorder and rebuild. `Changed`
  reports it. `SortBy *[]SortOrder` for multi-column (Cmd/Shift-click adds; headers
  show direction and precedence).
- `RowHeight float32` for dense rows; `RoundRows bool` for rounded corners.
- `Columns TableLayout` persists user order/widths by `ID`; `Changed` fires once
  per gesture (also after a double-click fit).
- `HeaderMenu func(m *Menu, col int)`, `CellMenu func(m *Menu, row, col int)`,
  `HeaderCell func(cell Element, col int)`.
- Cell blocks: `CellSelect bool` + `Cells ColumnRange`, `CellsStyle`; read with
  `CellSelected(row,col)`, `SelectedColumns()`, `SelectColumns(from,to)`.
- In-place rename: `ui.EditableText` in column 0 (Return / F2).

**List vs Table:** same engine and state. `List` = one builder per row, free-form.
`Table` adds column headers + `cell(row, col)`: aligned/resizable/reorderable
columns, sticky spanning headers, sorting, saved layout, cell-block selection.
Tree in columns ⇒ `OutlineTable`.

## 10. Tree / Outline

```go
func Tree(c *Context, fn func()) Element                    // builds ALL items
func TreeItem(c *Context, label string, open *bool, children func()) Element
func Outline[K comparable](c *Context, s *OutlineState[K], roots []K,
        children func(K) []K, row func(K)) Element           // virtualized
func OutlineTable[K comparable](c *Context, s *OutlineState[K], columns []TableColumn,
        roots []K, children func(K) []K, cell func(K, int)) Element
```

```go
item := func(path, label string, open *bool, children func()) {
    if ui.TreeItem(c, label, open, children).Selected(app.chosen == path).Clicked() {
        app.chosen = path
    }
}
ui.Tree(c, func() {
    item("src", "src", &app.srcOpen, func() { item("src/main.go", "main.go", nil, nil) })
    item("go.mod", "go.mod", nil, nil)
})
```

Arrow toggles open; clicking elsewhere or Enter fires `Clicked` — select it
yourself with `.Selected(...)`. `nil` children func or `nil` `open` = leaf. Keys:
Up/Down move, Home/End ends, Right expands, Left collapses, type-ahead.
`Tree` builds **every** item — use `Outline` for thousands of nodes.

## 11. Badge / Avatar

```go
func Badge(c *Context, text string) Element
func Avatar(c *Context, name string, image *Bitmap) Element   // initials from name
```

```go
ui.Badge(c, "New").Background(t.Accent).TextColor(t.AccentText)
ui.Avatar(c, "Ada Lovelace", nil).Size(28, 28)
```

## 12. Progress / Spinner / Meter

```go
func Progress(c *Context, value float64) Element     // 0..1; negative = indeterminate
func Spinner(c *Context) Element                     // always indeterminate
func Meter(c *Context, value, lo, hi float64, levels *MeterLevels) Element
```

```go
ui.Progress(c, float64(done)/float64(total)).Label("Uploading")
ui.Progress(c, -1).Label("Connecting")     // indeterminate moving block
ui.Spinner(c).Label("Loading")             // Size(32,32) for empty states
ui.Meter(c, disk.Used, 0, disk.Size,
    &ui.MeterLevels{Warning: 0.8*disk.Size, Critical: 0.95*disk.Size}).Label("Disk")
```

`Progress`: fill = accent or `TextColor`, track = `Border` or `Background`;
`Reverse()` fills from the other side; it fills its column (give `Width` in a
row); indeterminate animation is paint-only (no view re-run). `Spinner` spins even
under reduced-motion. `Meter` is a *level* indicator:
`MeterLevels{Warning, Critical float64; NormalColor, WarningColor, CriticalColor
Color}` — `Critical < Warning` makes low values the bad ones (battery).

## 13. Toast — imperative, from anywhere on the `*Context`

**Toasts are pushed, not declared:**

```go
func (c *Context) Toast(message string)
func (c *Context) ToastAction(message, label string, action func())
func (c *Context) AddToast(t Toast) (id string)
func (c *Context) CloseToast(id string)        // "" closes all
```

```go
if ui.Button(c, "Save").Clicked() { app.save(); c.Toast("Saved") }

if ui.Button(c, "Delete").Clicked() {
    app.trash(note)
    c.ToastAction("Note deleted", "Undo", func() { app.restore(note) })
}

id := c.AddToast(ui.Toast{Title: "Uploading", Description: "report.pdf", Timeout: -1})
c.CloseToast(id)                                // when the upload finishes
```

`Toast`: `ID` (same ID updates & re-shows), `Title`, `Description`, `Type`
(`"error"`, `"success"`), `Timeout` (`0` ⇒ 4s, negative ⇒ sticky), `Action` +
`OnAction` (runs it and closes), `OnClose`, `Data`. Latest three stack; timer
pauses on hover/focus, continues in background; Escape closes the focused toast.
Because it's one call on `c` it works from a click handler, a dialog button, or
`win.Update`. Custom look: call `ui.ToastViewportBase(c, func(viewport Element,
toasts []Toast){...})` once in the view, building each with `ui.ToastBase(c, t)` →
`ToastParts{Root Element; ActionButton(); CloseButton(); Left() time.Duration}`.

## 14. Dialog / AlertDialog

**You own a `*bool`.** No dialog stack, no `Open()` — it renders iff the pointer
is true.

```go
func Modal(c *Context, open *bool, fn func()) Element
func AlertDialog(c *Context, open *bool, title, message string, buttons ...string) int
func DialogBase(c *Context, open *bool, fn func(backdrop, panel Element)) Element
```

`Modal` — Escape and outside-click set `*open = false`:

```go
if ui.Button(c, "Rename…").Clicked() { app.renaming = true }
ui.Modal(c, &app.renaming, func() {
    ui.Text(c, "Rename").Bold()
    if ui.TextInput(c, &app.name).AutoFocus().Submitted() { app.rename(); app.renaming = false }
    ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
        if ui.Button(c, "Cancel").Clicked() { app.renaming = false }
        if ui.PrimaryButton(c, "Rename").Clicked() { app.rename(); app.renaming = false }
    })
})
```

Focus is trapped (first focusable or `AutoFocus`), Tab cycles, background inert,
focus returns to the opener. Veto a dismissal (unsaved changes):

```go
if ui.Modal(c, &app.editing, app.editor).Dismissed() && app.unsaved {
    app.editing = true                                  // keep it open
}
```

`AlertDialog` — last button is the default (accent, focused, Enter). Escape
**always** cancels and returns `ui.AlertDismissed` (-2); outside clicks do
nothing; otherwise `-1` for no action, else the button index:

```go
if ui.Button(c, "Delete").Clicked() { app.asking = true }
switch ui.AlertDialog(c, &app.asking, `Delete "Notes"?`, "You can't undo this.",
                      "Cancel", "Delete") {
case 1:                 // "Delete"
    app.delete()
case ui.AlertDismissed:
}
```

Must-answer alert (Escape doesn't close it):

```go
if ui.AlertDialog(c, &app.updating, "Update required",
    "Restart to finish updating.", "Restart") == ui.AlertDismissed {
    app.updating = true
}
```

`DialogBase` — themed, no look; `backdrop` centers and dims, `panel` is the surface:

```go
ui.DialogBase(c, &app.confirm, func(backdrop, panel ui.Element) {
    backdrop.Background(ui.RGBA(0, 0, 0, 0.5))
    panel.Width(360).Padding(24).Radius(16).Background(c.Theme().Background)
})
```

**Native destructive confirm** — blocks, callable from any goroutine:

```go
res, err := mygo.Dialog.Message(mygo.MessageOptions{
    Type: mygo.MessageWarning, Title: "Delete project?",
    Message: "This cannot be undone.", Detail: "3 files will be removed.",
    Buttons: []string{"Cancel", "Delete"},
    DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
})
if err == nil && res.Button == 1 { app.delete() }
```

`CancelButton: -1` ⇒ Escape does nothing (`Button -1` on Linux/Windows). Also
`mygo.Dialog.Error(title, content)`, `.Open(...)`, `.Save(...)`. Use in-window
`AlertDialog`/`Modal` for themed UI; `mygo.Dialog.Message` for OS-level confirms
or from a goroutine.

## 15. Popover / Tooltip

```go
func Popover(c *Context, anchor Element, open *bool, fn func()) Element
func PopoverBase(c *Context, anchor Element, open *bool, fn func(panel Element)) Element
func TooltipBase(c *Context, anchor Element, fn func(tip Element)) Element
```

```go
more := ui.Button(c, "More ▾")
if more.Clicked() { app.menu = !app.menu }
ui.Popover(c, more, &app.menu, func() {
    if ui.Button(c, "Rename").Clicked() { app.menu = false; app.renaming = true }
})
ui.Button(c, "Hover me").Tooltip("Saves the document")   // hover or keyboard focus
```

Outside-press/Escape sets `*open = false`, and that press **continues** to what's
under it. Flips above when there's no room; follows the anchor on scroll.

## 16. Menus

```go
func MenuButton(c *Context, label string, build func(m *Menu)) Element
func PopoverMenuButton(c *Context, label string, build func(m *Menu),
        options ...PopoverMenuButtonOptions) Element
// element-level: el.Menu(b), el.PopoverMenu(b), el.PopoverMenuButton(b), el.ContextMenu(b)
```

`m.Item(...)` returns `*MenuItem`; **`Chosen()` is the result** — the builder
re-runs the frame after a selection.

```go
ui.MenuButton(c, "Actions", func(m *ui.Menu) {
    if m.Item("New File").Chosen() { app.newFile() }
    m.Item("Rename").Shortcut(ui.Cmd, ui.KeyR).Chosen()
    m.Item("Locked").Disabled(true)
    m.Separator()
    m.Submenu("Export", func(sub *ui.Menu) {
        if sub.Item("PDF").Chosen() { app.exportPDF() }
    })
    m.Item("Delete").Danger().Chosen()
})
```

`Menu`: `Item(label)`, `Separator()`, `Submenu(label, build)`, `MaxWidth(w)`,
`EditItems()` (platform cut/copy/paste). `MenuItem`: `Chosen`, `Checked`, `Radio`,
`Disabled`, `Shortcut(mods,key)` (display only), `Icon`, `IconColor`, `Subtitle`,
`Trailing`, `Tooltip`, `Danger`, `ID`, `Highlighted`, `Content(func(MenuItemContent))`.

```go
ui.Text(c, "right click me").ContextMenu(func(m *ui.Menu) {
    if m.Item("Inspect").Chosen() { app.inspect() }
})
```

In a row builder, record the index and mutate **after** the row loop — removing a
row mid-loop corrupts the build. `PopoverMenuButtonOptions{At, Self Anchor; Open
*bool; Panel func(Element); Item func(Element)}` — `At`/`Self` place it, `Open`
lets you drive it, `Panel`/`Item` restyle. `c.SetMenuPresentation(ui.PopoverMenus)`
draws *every* menu in-window. `ContextMenuBase(c, target, *bool) MenuParts` =
fully custom no-look menu.

## 17. Icons — SVG supplied by you

**Icons are parsed SVG bytes; there is no built-in set or icon-font API.**

```go
//go:embed icons/save.svg
var saveSVG []byte
var save = ui.MustParseSVG(saveSVG)        // parse ONCE, package level

ui.Icon(c, save)                                   // height = FontSize, keeps aspect
ui.Icon(c, save).FontSize(24).TextColor(t.Danger)
ui.Icon(c, save).Size(16, 16)
```

- Color comes from the **text color** (inherited) — the SVG's own colors are
  ignored, so one monochrome SVG serves light/dark and any accent.
- Height defaults to the font size; `.Size(w,h)` overrides; never stretches.
- `.Rotate(deg)`; spin with `icon.Rotate(icon.Loop("spin", time.Second, ui.Linear) * 360)`.
- Icons are decorative: invisible to assistive tech unless `.Label(...)`; name the
  **button** for icon-only buttons.
- `ui.SVG`: `ParseSVG([]byte)`, `.Size()`, `.Unsupported()`, `.WithDark(bool)`,
  `.Image(w,h,color) *image.RGBA`. Colored art ⇒ `ui.Image` (keeps its own colors).
- Supports paths/basic shapes, groups/use/symbol, transforms, gradients, strokes &
  dashes, opacity, clip paths, masks, markers, text, CSS by type/class/id, `var()`,
  `prefers-color-scheme`. **Not** embedded images, patterns, filters.
- **Where to get icons:** Lucide, Heroicons, Tabler, Material Symbols ship plain
  `.svg` files — embed the ones you need; prefer single-path outline icons so they
  tint correctly.

**Menu icons** come from `mygo`, not `ui`:

```go
icon, err := mygo.NewMenuIconSVG(trash, true)     // template = tint with text color
m.Item("Delete").Icon(icon).IconColor(t.Danger)   // IconColor: in-window menus only
```

## 18. Image / Link / Scroll

```go
func Image(c *Context, src ImageSource) Element
func DecodeImage(data []byte) (ImageSource, error)
func Link(c *Context, label string, url string) Element
func Scroll(c *Context) Element            // + ScrollBoth(c), ScrollHorizontal(c)
```

```go
if img, err := ui.DecodeImage(png); err == nil {
    ui.Image(c, img).Size(64, 64).Fit(ui.Contain)   // Contain|Cover|Fill|...
}
ui.Link(c, "Docs", "https://example.com")
ui.Link(c, "Settings", "/settings")                 // in-app route
ui.Scroll(c).Height(200).Children(func() { /* tall content */ })
ui.Scroll(c).TrackScroll(&app.sc)                   // ScrollState{X,Y,MaxX,MaxY}
app.sc.ScrollTo(0, 120, ui.Smooth)
```

`List`/`Table`/`GridView` scroll themselves — don't wrap them in `Scroll`.

## 19. Form / Accordion / Collapsible

```go
func Form(c *Context, fn func()) Element
func Field(c *Context, label string, fn func()) Element
func Fieldset(c *Context, legend string, fn func()) Element
func Accordion(c *Context, fn func()) Element
func AccordionItem(c *Context, title string, open *bool, fn func()) Element
func Collapsible(c *Context, label string, open *bool, fn func()) Element
```

```go
// In a Form, labels get their own right-aligned column (macOS style);
// outside one they sit above the control.
ui.Form(c, func() {
    ui.Field(c, "Name", func() { ui.TextInput(c, &app.name) })
    ui.Field(c, "SDK", func() { ui.Select(c, &app.sdk, sdkList) })
    ui.Field(c, "Auto update", func() { ui.Switch(c, &app.auto) })
    ui.Fieldset(c, "Notifications", func() {
        ui.Field(c, "Email", func() { ui.Checkbox(c, &app.mail, "Weekly summary") })
    }).Disabled(!app.signedIn)
})

ui.Accordion(c, func() {
    ui.AccordionItem(c, "General", &app.general, func() { app.generalSettings(c) })
    ui.AccordionItem(c, "Privacy", &app.privacy, func() { app.privacySettings(c) })
})
// one-at-a-time: mirror the open state yourself
for i, s := range sections {
    open := app.section == i
    ui.AccordionItem(c, s.Title, &open, s.Build).OnChange(func() {
        app.section = -1; if open { app.section = i }
    })
}

ui.Collapsible(c, "Advanced", &app.advanced, func() {
    ui.Checkbox(c, &app.verbose, "Verbose logging")
})
```

`Field` names the first focusable element it builds and focuses it when the label
is clicked; add `.Description(s)` / `.Error(msg)` on the Field.

## 20. Layout & theme modifiers used above

`ui.Column`, `ui.Row`, `ui.Box`, `ui.Grid`, `ui.Spacer`, `ui.Divider`,
`ui.Toolbar`, `ui.Scroll`. Box model `.Padding(v...)` / `.Margin(v...)` (+ `X`/`Y`/
`Start`/`End`), `.Width/.Height/.MinWidth/.MaxWidth/.Size(w,h)`,
`.Fill()/.FillWidth()/.FillHeight()`, `.Grow(f)`. Layout `.Justify(ui.Start|
Center|End|SpaceBetween)`, `.AlignItems`, `.AlignSelf`, `.Center()`, `.Gap(8)`.
Paint `.Background`, `.Border(w,col)`, `.Radius(r...)`, `.Shadow(...)`, `.Opacity`.
Text `.TextColor`, `.FontSize`, `.Bold()`, `.SingleLine()`, `.MaxLines(n)`,
`.Ellipsis(s)`, `.TruncateStart()`. A11y `.Label`, `.Description`, `.LabelledBy`,
`.DescribedBy`, `.Role(r)`. Identity `.Key(any)`, `.Bind(*Handle)`/`.Ref(*Handle)`.

`c.Theme()`: `Text`, `TextMuted`, `Background`, `Surface`, `SurfaceHover`,
`SurfacePressed`, `Border`, `Accent`, `AccentText`, `Danger`, `Warning`, `Success`,
`Inverse`, `InverseText`, `Radius`, `Space(n)`.

## 21. Recipe — compile-verified

This block is `_probe\scratch-widgets\t08_docrecipe\main.go`, extracted verbatim
from this document and built. Shows: search field filtering a virtualized list,
rows with avatar + title + subtitle + badge + action button, a select for an SDK
version, a toast after an action, and a confirm dialog before a destructive one.

```go
package main

import (
    "fmt"
    "log"
    "strings"

    "github.com/egoist/mygo"
    "github.com/egoist/mygo/ui"
)

type tool struct{ ID, Title, Subtitle string; New bool }

type app struct {
    tools   []tool
    query   string
    sdks    []string
    sdk     string
    list    ui.ListState
    sel     int
    confirm bool
}

// The filter is plain app code; the list only builds what's visible.
func (a *app) filtered() []tool {
    if a.query == "" { return a.tools }
    q := strings.ToLower(a.query)
    out := make([]tool, 0, len(a.tools))
    for _, t := range a.tools {
        if strings.Contains(strings.ToLower(t.Title), q) ||
            strings.Contains(strings.ToLower(t.Subtitle), q) {
            out = append(out, t)
        }
    }
    return out
}

func (a *app) View(c *ui.Context) {
    rows := a.filtered()
    t := c.Theme()

    // Key by ID so the selection follows the tool as the filter changes rows.
    if len(rows) > 0 {
        a.list.Key = func(i int) any { return rows[i].ID }
    } else {
        a.list.Key = nil
    }
    a.list.Selected = &a.sel

    ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {

        // --- toolbar: search + SDK select ---
        ui.Row(c).Gap(8).Children(func() {
            if ui.SearchField(c, &a.query).Placeholder("Search tools").
                Label("Search tools").Changed() {
                a.sel = -1 // filtered rows are a new set
            }
            ui.Select(c, &a.sdk, a.sdks).Label("SDK version").Width(140)
        })

        // --- virtualized list: icon + title + subtitle + badge + action ---
        ui.List(c, &a.list, len(rows), func(i int) {
            r := rows[i]
            ui.Row(c).Gap(10).Padding(8, 10).Children(func() {
                ui.Avatar(c, r.Title, nil).Size(28, 28)
                ui.Column(c).Grow(1).Gap(2).Children(func() {
                    ui.Text(c, r.Title).SingleLine()
                    ui.Text(c, r.Subtitle).FontSize(11).
                        TextColor(t.TextMuted).SingleLine()
                })
                if r.New { ui.Badge(c, "new") }
                if ui.Button(c, "Install").Clicked() {
                    c.ToastAction(r.Title+" queued", "Undo", func() {})
                }
            })
        }).Grow(1)

        // --- empty state ---
        if len(rows) == 0 {
            ui.Text(c, "No tools match "+a.query).TextColor(t.TextMuted).Padding(12)
        }

        // --- destructive action behind a confirm dialog ---
        target := a.sel
        if target >= 0 && target < len(rows) {
            if ui.Button(c, "Uninstall "+rows[target].Title).Clicked() {
                a.confirm = true
            }
        }
        switch ui.AlertDialog(c, &a.confirm,
            "Uninstall this tool?", "The tool and its cache are removed.",
            "Cancel", "Uninstall") {
        case 1: // "Uninstall"
            c.Toast(fmt.Sprintf("Uninstalled %s", rows[target].Title))
            if target >= 0 && target < len(rows) {
                for i := range a.tools {
                    if a.tools[i].ID == rows[target].ID {
                        a.tools = append(a.tools[:i], a.tools[i+1:]...)
                        break
                    }
                }
            }
        }
    })
}

func main() {
    a := &app{
        sdks: []string{"SDK 34", "SDK 35", "SDK 36"},
        sdk:  "SDK 36",
        sel:  -1,
        tools: []tool{
            {ID: "adb", Title: "adb", Subtitle: "Android Debug Bridge", New: true},
            {ID: "emu", Title: "emulator", Subtitle: "Android emulator"},
            {ID: "fastboot", Title: "fastboot", Subtitle: "Flash firmware", New: true},
        },
    }
    mygo.App.WhenReady(func() {
        mygo.NewWindow(mygo.WindowOptions{
            Title: "Tool Manager", Width: 720, Height: 520,
            MinWidth: 480, MinHeight: 360,
            Content: ui.View(a.View),
        })
    })
    if err := mygo.App.Run(); err != nil { log.Fatal(err) }
}
```

## 22. Gotchas

1. **`*ui.Context` first** in every constructor — `ui.Text("x")` doesn't compile.
2. **`Element` dies before the next build pass.** Read `Clicked()`/`Changed()`
   immediately; never store an `Element`. Persistence ⇒ `Handle`.
3. **Toolchain:** mygo's `go.mod` says `go 1.27.3`; with `GOTOOLCHAIN=auto` Go
   fetches 1.27.4 itself. In PowerShell quote the flag:
   `go mod edit "-require=github.com/egoist/mygo@v0.9.2"` (a bare `@` gets mangled).
4. **`go mod tidy` drops the require** if nothing imports `ui` yet — re-add it
   after writing a file that imports `ui`.
5. **`List`'s row callback is variadic** — `List(c, s, n)` without one is legal,
   then configure and `.Rows`.
6. **Selection is written during the build.** With `Key`, `*Selected` becomes `-1`
   in the same pass if the chosen item left the rows — handle `-1` downstream.
7. **Use `RowClicked`/`RowDoubleClicked` on a child** to keep row selection;
   `Clicked` on the child swallows the click.
8. **`Pressed`/`Hovered` on a child breaks selection** — use `ListRow` from `.Rows`.
9. **`Password()` is per-frame**, not a flag: reveal = stop calling it.
10. **`Submitted` is single-line only**; Enter in a textarea ⇒ `OnTextKey`.
11. **Sorting a Table is your job** — `ListState.Sort` records intent only.
12. **`ui.Range` / `ui.TextIcon` don't exist.** `Range` is `Element.Range(lo,hi,v)`;
    menu icons are `mygo.NewMenuIcon(SVG)`.
13. **`PopoverMenuButtonOptions` has `At`/`Self`, not `Align`.**
14. **`AlertDialog`: `-1` = nothing, `-2` = `ui.AlertDismissed` (Escape).**
15. **Outside-click closes `Modal`/`Popover`** and writes `*open=false`; only
    `AlertDialog` ignores outside clicks.
16. **Parse SVG once at package level**, never inside the view.
17. **Set `ListState` fields before building** the list.
18. **Removing rows while iterating** a row builder corrupts the build — record
    the index and mutate after the list/table call returns.
