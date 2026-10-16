package main

import "github.com/egoist/mygo/ui"

// desktopSplit leaves sizing, dragging and keyboard access to MyGo. The
// only extra gesture is Retty's double-click to restore the default size.
func desktopSplit(c *ui.Context, k *colors, size *float32, vertical, percent bool, reset func(), first func() ui.Element, second func()) ui.Element {
	original := c.Theme()
	theme := *original
	theme.Border = k.headerBorder
	// The focused pane paints its outline over the shared divider.
	theme.Accent = k.headerBorder
	c.SetTheme(&theme)
	defer c.SetTheme(original)
	var leading, split ui.Handle
	buildFirst := func() {
		c.SetTheme(original)
		defer c.SetTheme(&theme)
		first().Fill().Bind(&leading)
	}
	buildSecond := func() {
		c.SetTheme(original)
		defer c.SetTheme(&theme)
		second()
	}
	constructor := ui.Split
	switch {
	case vertical && percent:
		constructor = ui.SplitVerticalPercent
	case percent:
		constructor = ui.SplitPercent
	case vertical:
		constructor = ui.SplitVertical
	}
	e := constructor(c, size, buildFirst, buildSecond).MinWidth(0).MinHeight(0).Bind(&split)
	e.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputPointerDown || ev.Button != 0 || ev.Clicks != 2 {
			return false
		}
		pane, bounds := leading.Bounds(c), split.Bounds(c)
		position, divider := ev.X, pane.X+pane.W-bounds.X+.5
		if vertical {
			position, divider = ev.Y, pane.Y+pane.H-bounds.Y+.5
		}
		if abs(position-divider) > 3 {
			return false
		}
		reset()
		return true
	})
	return e
}
