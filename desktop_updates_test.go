package main

import (
	"slices"
	"testing"
)

func TestCheckUpdatesWithoutTerminalWindow(t *testing.T) {
	// The updater must remain available after closing the last terminal window.
	original := cmdCheckUpdates.Run
	t.Cleanup(func() { cmdCheckUpdates.Run = original })
	called := false
	cmdCheckUpdates.Run = func(*App) { called = true }
	a := &App{}
	a.run(&cmdCheckUpdates)
	if !called || a.win != nil || a.client != nil {
		t.Fatal("checking updates should open only the updater, without a session window")
	}
	if !slices.Contains(paletteCommands, &cmdCheckUpdates) {
		t.Fatal("checking updates is missing from the command palette")
	}
	menu := a.menu()
	for _, item := range menu.Items()[0].Submenu {
		if item.Label == cmdCheckUpdates.Title && item.Click != nil {
			return
		}
	}
	t.Fatal("checking updates is missing from the app menu")
}
