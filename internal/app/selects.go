package app

import "github.com/egoist/mygo/ui"

// Every selector in EnvMgr draws the same vector chevron, in a box of fixed
// size at the trigger's trailing edge. A glyph such as "⌄" depended on the UI
// font's baseline and side bearings, so the arrow sat low and off centre; a
// drawn icon and a sized box keep it centred on one-line and two-line labels.
//
// It also makes the alias/value split possible: ui.Select only carries the
// strings it shows, so a select whose visible name differs from the value it
// stores (a remote's alias against its URL, a branch's label against its full
// ref) needs this base.
type selectChoice struct{ Name, Value, Detail string }

// stringSelect is selectControl for a plain list of labels whose value is the
// label itself, the shape ui.Select had.
func (a *App) stringSelect(c *ui.Context, selected *string, options []string, label string, grow bool) bool {
	choices := make([]selectChoice, len(options))
	choice := selectChoice{Name: *selected, Value: *selected}
	for i, option := range options {
		choices[i] = selectChoice{Name: option, Value: option}
	}
	if !a.selectControl(c, &choice, choices, label, false, grow) {
		return false
	}
	*selected = choice.Value
	return true
}

// selectControl draws a select and reports a new choice. grow widens the
// trigger to fill a row; in a column it stretches to the column's width
// without it, so the pin pickers inside a card stay one line high.
func (a *App) selectControl(c *ui.Context, selected *selectChoice, options []selectChoice, label string, disabled, grow bool) bool {
	t := c.Theme()
	selectBox := ui.SelectBase(c, selected)
	trigger := selectBox.Trigger.MinHeight(32).Padding(7, 10).Radius(8).
		Border(1, t.Border).Background(t.Surface).Gap(10).AlignItems(ui.Center).
		Label(label).Disabled(disabled || len(options) == 0)
	if grow {
		trigger.Grow(1).MinWidth(0)
	}
	trigger.Children(func() {
		ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
			name := ui.Text(c, selected.Name).FontSize(12).SingleLine().Ellipsis("…")
			if selected.Detail != "" {
				name.Bold()
				ui.Text(c, selected.Detail).FontSize(10).TextColor(t.TextMuted).
					SingleLine().Ellipsis("…")
			}
		})
		// The arrow keeps its own 16x16 box, centred, at the trailing edge:
		// it never takes part in the text's line height or its ellipsis.
		ui.Box(c).Size(16, 16).Shrink(0).Center().Children(func() {
			ui.Icon(c, icChevronDown).Size(16, 16).Shrink(0).TextColor(t.TextMuted)
		})
	})
	selectBox.Popup(func(panel ui.Element) {
		panel.Padding(4).Radius(9).Background(t.Surface).Border(1, t.Border).
			MaxHeight(320).Children(func() {
			ui.Scroll(c).MaxHeight(300).Gap(2).Children(func() {
				for _, option := range options {
					option := option
					item := selectBox.Item(option).Padding(8, 10).Radius(6)
					fg, muted := t.Text, t.TextMuted
					if item.Highlighted() {
						item.Background(t.Accent)
						fg = t.AccentText
						muted = fg.Alpha(0.8)
					} else if option == *selected {
						item.Background(t.SurfaceHover)
					}
					item.Children(func() {
						ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
							name := ui.Text(c, option.Name).FontSize(11.5).TextColor(fg).
								SingleLine().Ellipsis("…")
							if option.Detail != "" {
								name.Bold()
								ui.Text(c, option.Detail).FontSize(10).TextColor(muted).
									SingleLine().Ellipsis("…")
							}
						})
					})
				}
			})
		})
	})
	return trigger.Changed()
}
