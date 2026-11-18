package app

import (
	"image"
	"image/color"
	"math"
	"testing"

	"envmgr/internal/core"
	"github.com/egoist/mygo/ui"
)

// inkBox is the bounding box, in DIPs, of the pixels of img inside bounds r
// that differ from the region's dominant color, which is the surface behind
// them. The border of a control is left out by insetting the region, so what
// is measured is the glyph alone.
type inkBox struct{ minX, minY, maxX, maxY float32 }

func (b inkBox) center() (float32, float32) {
	return (b.minX + b.maxX) / 2, (b.minY + b.maxY) / 2
}

func (b inkBox) width() float32  { return b.maxX - b.minX }
func (b inkBox) height() float32 { return b.maxY - b.minY }

func measureInk(img *image.RGBA, r ui.Rect, scale float32) inkBox {
	toPixel := func(v float32) int { return int(math.Round(float64(v * scale))) }
	x0, y0 := toPixel(r.X), toPixel(r.Y)
	x1, y1 := toPixel(r.X+r.W), toPixel(r.Y+r.H)
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > img.Bounds().Dx() {
		x1 = img.Bounds().Dx()
	}
	if y1 > img.Bounds().Dy() {
		y1 = img.Bounds().Dy()
	}
	counts := map[color.RGBA]int{}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			counts[color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}]++
		}
	}
	var bg color.RGBA
	most := -1
	for c, n := range counts {
		if n > most {
			bg, most = c, n
		}
	}
	diff := func(c color.RGBA) int {
		d := func(a, b uint8) int {
			if a > b {
				return int(a - b)
			}
			return int(b - a)
		}
		return max(d(c.R, bg.R), max(d(c.G, bg.G), d(c.B, bg.B)))
	}
	box, found := inkBox{}, false
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if diff(color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}) < 12 {
				continue // the surface behind the glyph, or its antialiasing tail
			}
			fx, fy := float32(x)/scale, float32(y)/scale
			if !found {
				box = inkBox{fx, fy, fx, fy}
				found = true
				continue
			}
			box.minX, box.minY = min(box.minX, fx), min(box.minY, fy)
			box.maxX, box.maxY = max(box.maxX, fx), max(box.maxY, fy)
		}
	}
	if !found {
		return inkBox{}
	}
	// A pixel covers [x, x+1), so the glyph ends one pixel past its last.
	box.maxX += 1 / scale
	box.maxY += 1 / scale
	return box
}

// near reports whether got is within tolerance DIPs of want.
func near(got, want, tolerance float32) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tolerance
}

// TestSelectChevronIsCenteredBesideItsText pins the placement Sir asked for:
// the arrow sits vertically centred in the trigger, flush against its
// trailing padding, and never taller than the text line beside it. The old
// arrow was a "⌄" glyph, whose font baseline put it low and whose side
// bearings pushed it off the edge.
func TestSelectChevronIsCenteredBesideItsText(t *testing.T) {
	a := newTestApp(t)
	for _, grow := range []bool{false, true} {
		choice := selectChoice{Name: "origin", Value: "https://github.com/acme/app.git", Detail: "https://github.com/acme/app.git"}
		options := []selectChoice{choice, {Name: "gitee", Value: "https://gitee.com/acme/app.git"}}
		tt := ui.NewTester(func(c *ui.Context) {
			if grow {
				ui.Row(c).Width(420).Children(func() { a.selectControl(c, &choice, options, "远程仓库", false, true) })
				return
			}
			ui.Column(c).Width(420).Children(func() { a.selectControl(c, &choice, options, "远程仓库", false, false) })
		}, 460, 120)
		tt.SetScale(1)
		tt.Frame()
		info, ok := tt.AccessByRole(ui.RolePopUpButton, "远程仓库")
		if !ok {
			t.Fatalf("the select has no pop-up button for its label (grow=%v)", grow)
		}
		if !near(info.Bounds.W, 420, 1) {
			t.Fatalf("trigger width %v, want 420 (grow=%v)", info.Bounds.W, grow)
		}
		// Only the trailing strip, inset past the trigger's border.
		strip := ui.Rect{
			X: info.Bounds.X + info.Bounds.W - 34, Y: info.Bounds.Y + 3,
			W: 30, H: info.Bounds.H - 6,
		}
		ink := measureInk(tt.Image(), strip, 1)
		if ink.width() == 0 {
			t.Fatalf("no arrow drawn at the trigger's trailing edge (grow=%v)", grow)
		}
		_, inkY := ink.center()
		_, triggerY := inkBox{info.Bounds.Y, info.Bounds.Y, info.Bounds.Y, info.Bounds.Y + info.Bounds.H}.center()
		if !near(inkY, triggerY, 1.5) {
			t.Errorf("the arrow's centre is %v, the trigger's %v (grow=%v): %v tall",
				inkY, triggerY, grow, ink.height())
		}
		right := info.Bounds.X + info.Bounds.W
		if !near(ink.maxX, right-12.5, 3) {
			t.Errorf("the arrow ends %v from the trigger's right edge, want 10-14 (grow=%v)",
				right-ink.maxX, grow)
		}
		if ink.width() > 18 || ink.height() > 12 {
			t.Errorf("the arrow is %vx%v, too big for a 16x16 box (grow=%v)", ink.width(), ink.height(), grow)
		}
	}
}

// TestIconButtonGlyphIsCentered covers the buttons that show only an icon:
// open in the file manager, open the website, copy a path. A button keeps its
// label's horizontal padding unless it is cleared, which pushed the icon off
// centre to the right.
func TestIconButtonGlyphIsCentered(t *testing.T) {
	a := newTestApp(t)
	cases := []struct {
		icon  *ui.SVG
		label string
	}{
		{icFolder, "打开目录"},
		{icExternal, "打开官网"},
		{icCopy, "复制路径"},
		{icTrash, "删除该版本"},
	}
	for _, tc := range cases {
		tc := tc
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Gap(8).Children(func() {
				a.iconButton(c, tc.icon, tc.label, false)
			})
		}, 160, 60)
		tt.SetScale(1)
		tt.Frame()
		info, ok := tt.AccessByRole(ui.RoleButton, tc.label)
		if !ok {
			t.Fatalf("%q has no button", tc.label)
		}
		if !near(info.Bounds.W, 28, 0.5) || !near(info.Bounds.H, 28, 0.5) {
			t.Fatalf("%q is %vx%v, want a 28x28 square", tc.label, info.Bounds.W, info.Bounds.H)
		}
		// Inset past the button's rounded background so only the glyph is ink.
		inner := ui.Rect{
			X: info.Bounds.X + 3, Y: info.Bounds.Y + 3,
			W: info.Bounds.W - 6, H: info.Bounds.H - 6,
		}
		ink := measureInk(tt.Image(), inner, 1)
		if ink.width() == 0 {
			t.Fatalf("%q draws no icon", tc.label)
		}
		inkX, inkY := ink.center()
		buttonX := info.Bounds.X + info.Bounds.W/2
		buttonY := info.Bounds.Y + info.Bounds.H/2
		if !near(inkX, buttonX, 1) {
			t.Errorf("%q's icon centre is %v, the button's %v: it sits %v off centre",
				tc.label, inkX, buttonX, inkX-buttonX)
		}
		if !near(inkY, buttonY, 1) {
			t.Errorf("%q's icon centre is %v, the button's %v: it sits %v off centre",
				tc.label, inkY, buttonY, inkY-buttonY)
		}
		if ink.width() > 16 || ink.height() > 16 {
			t.Errorf("%q's icon is %vx%v, larger than the 14x14 it is given",
				tc.label, ink.width(), ink.height())
		}
	}
}

// TestPinPickerKeepsOneLineHeight guards the select inside an SDK card: it
// stretches to the column's width but must not grow vertically, which a
// flexing trigger in a column does.
func TestPinPickerKeepsOneLineHeight(t *testing.T) {
	a := newTestApp(t)
	a.pinBuf = make([]string, len(core.Kinds))
	p := core.NewProject(t.TempDir())
	kind := core.Node
	a.pinBuf[kindIndexOf(kind)] = "不固定（跟随项目文件）"
	installed := []core.Installed{{Kind: kind, Version: "24.13.0"}, {Kind: kind, Version: "18.16.0"}}
	r := core.Resolution{Kind: kind}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(320).Children(func() {
			a.kindCard(c, p, kind, r, installed, false)
		})
	}, 360, 400)
	tt.SetScale(1)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RolePopUpButton, core.Info(kind).Name+" 版本")
	if !ok {
		t.Fatal("the pin picker has no pop-up button")
	}
	if info.Bounds.H > 44 {
		t.Errorf("the pin picker is %v tall, taller than its one line of text: %v",
			info.Bounds.H, info.Bounds)
	}
	if info.Bounds.H < 24 {
		t.Errorf("the pin picker is %v tall, too short to click: %v", info.Bounds.H, info.Bounds)
	}
}
