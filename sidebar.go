package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) sidebarLayout(c *ui.Context, k *colors, bar ui.TitleBar) {
	width, _ := c.Size()
	sideWidth := float32(224)
	if width < 720 {
		sideWidth = 188
	}
	side, border := k.track, k.headerBorder
	if !c.Theme().Dark {
		side, border = ui.Hex("#f0f1f4"), k.panelBorder
	}
	ui.Row(c.Key("sidebar-layout")).Fill().Children(func() {
		if !prefs.SidebarCollapsed {
			ui.Column(c.Key("tab-sidebar")).Label("Tab sidebar").Width(sideWidth).Shrink(0).FillHeight().
				Background(side).BorderWidth(0, 1, 0, 0).BorderColor(border).Children(func() {
				left := float32(0)
				if bar.Left > 0 {
					left = bar.Left + 8
				}
				ui.Row(c).Height(compactTitleH).FillWidth().Padding(0, 8, 0, left).AlignItems(ui.Center).DragWindow().Children(func() {
					ui.Spacer(c)
					a.sidebarToggle(c, k)
				})
				ui.Scroll(c.Key("sidebar-tabs")).Label("Sidebar tabs").Role(ui.RoleTabList).
					Grow(1).MinHeight(0).FillWidth().Padding(10, 8).Gap(4).Children(func() {
					for i, t := range slices.Clone(a.tabs) {
						a.sidebarTab(c, k, i, t)
					}
				})
			})
			a.sidebarVisibleTab = a.tab()
		}
		ui.Column(c.Key("sidebar-main")).Grow(1).Basis(0).MinWidth(0).FillHeight().Children(func() {
			a.sidebarTitleBar(c, k, bar)
			a.desktopContent(c, k)
		})
	})
}

func (a *App) sidebarToggle(c *ui.Context, k *colors) {
	label := "收起侧边栏"
	if prefs.SidebarCollapsed {
		label = "展开侧边栏"
	}
	if iconButton(c.Key("toggle-tab-sidebar"), k, "panel-left", label, 26, 15).
		Focusable().Shrink(0).Tooltip(label + "  ⌃⌘S").Clicked() {
		a.toggleTabSidebar()
		c.Invalidate()
	}
}

func (a *App) sidebarTitleBar(c *ui.Context, k *colors, bar ui.TitleBar) {
	left := float32(8)
	if prefs.SidebarCollapsed && bar.Left > 0 {
		left = bar.Left + 8
	}
	ui.Row(c).Height(compactTitleH).FillWidth().Padding(0, bar.Right, 0, left).Gap(6).
		Background(k.track).BorderWidth(0, 0, 1, 0).BorderColor(k.headerBorder).AlignItems(ui.Center).DragWindow().Children(func() {
		if prefs.SidebarCollapsed {
			a.sidebarToggle(c, k)
		}
		ui.Row(c).Grow(1).MinWidth(0).Children(func() {
			if t := a.tab(); t != nil {
				name, detail := t.label()
				if prefs.SidebarCollapsed && a.renaming == t {
					a.renameField(c, t, name)
				} else {
					ui.Text(c, strings.TrimSpace(name+" "+detail)).Label("Current session").FontSize(11.5).
						TextColor(k.textMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				}
			}
		})
		if compactTitleButton(c.Key("sidebar-new-tab"), k, "plus", "New Tab", false).BorderWidth(0).Tooltip("New Tab  ⌘T").Clicked() {
			a.newTab(a.currentDir())
		}
		a.filesToggle(c, k)
		if compactTitleButton(c.Key("desktop-connections"), k, "plug-connected", "连接", a.hasDesktopConnections()).BorderWidth(0).Tooltip("连接").Clicked() {
			a.showDesktopConnections(nil)
		}
	})
}

func (a *App) sidebarTab(c *ui.Context, k *colors, i int, t *Tab) {
	active := i == a.active
	name, detail := t.label()
	row := ui.Row(c.Key(t.ID)).Label(strings.TrimSpace(name+" "+detail)).Role(ui.RoleTab).Selected(active).
		FillWidth().Height(40).Shrink(0).Padding(0, 9).Gap(8).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if active {
		row.Background(k.panelSel)
		if a.sidebarVisibleTab != t {
			row.ScrollIntoView()
		}
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	row.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	if row.Clicked() {
		a.selectTab(i)
	}
	a.bindTabItem(k, row, i, t)
	if p, state := tabAgentState(t); p != nil {
		row.Tooltip(programOf(state.ID).Name + " · " + agentStateLabel(state))
	}
	row.Children(func() {
		glyph, color := "square-terminal", k.iconMuted
		if t.Host != nil {
			glyph = "monitor"
		} else if t.Focus != nil {
			prog := paneProgram(t.Focus)
			glyph, color = prog.Glyph, programIconColor(c, prog)
			if prog.Shell || t.Focus.info.Program == "" {
				color = k.iconMuted
			}
		}
		programIcon(c, glyph).Size(17, 17).TextColor(color).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			if a.renaming == t {
				a.renameField(c, t, name)
			} else {
				ui.Text(c, name).FontSize(12).FontWeight(500).TextColor(k.text).SingleLine().Ellipsis("…").FillWidth()
			}
			meta := detail
			if t.Host != nil {
				meta = t.Host.name()
				if detail != "" {
					meta += " · " + detail
				}
			}
			if meta != "" {
				ui.Text(c, meta).FontSize(10.5).TextColor(k.textFaint).SingleLine().Ellipsis("…").FillWidth()
			}
		})
		if p, state := tabAgentState(t); p != nil {
			a.agentIndicator(c, k, p, state)
		} else if t.attention() {
			ui.Box(c).Size(5, 5).Radius(3).Background(k.attention).Shrink(0)
		} else if a.tabBusy(c, t) {
			ui.Box(c).Size(5, 5).Radius(3).Background(k.busy).Shrink(0)
		}
		if row.Hovered() && len(a.tabs) > 1 {
			if iconButton(c.Key("close-tab"), k, "x", "Close Tab", 18, 11).Shrink(0).Tooltip("Close Tab").Clicked() {
				a.later(c, func() { a.closeTab(t) })
			}
		}
	})
}
