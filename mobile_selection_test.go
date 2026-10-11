package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"retty/internal/terminal"
)

func TestMobileSelectionMenuDismissesAndReopens(t *testing.T) {
	registerFonts()
	term, err := terminal.New(terminal.Options{Conn: nopConn{}, Font: terminal.Font{Family: termFont.Family, Size: 13}, Theme: lightTerm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { term.Close() })
	term.Feed([]byte("first selectable line\r\nsecond selectable line"))
	m := &mobileApp{term: term}
	outsideClicks := 0
	tt := ui.NewTester(func(c *ui.Context) {
		if ui.Button(c, "其他操作").Height(44).Clicked() {
			outsideClicks++
		}
		element := terminal.View(c, term).Label("测试终端").Grow(1).FillWidth()
		m.selectionMenu(c, element)
	}, 390, 300)
	selectAll := func() {
		term.SelectAll()
		tt.Frame()
		if !tt.HasText("复制") || !tt.HasText("全选") {
			t.Fatal("selection menu did not open")
		}
	}
	assertClosed := func() {
		t.Helper()
		for range 3 {
			tt.Frame()
			if tt.HasText("复制") || tt.HasText("全选") {
				t.Fatal("dismissed selection menu stayed open or reopened")
			}
		}
	}
	newSelection := func() {
		t.Helper()
		bounds, _ := tt.Find("测试终端")
		tt.Press(bounds.X+12, bounds.Y+6)
		tt.Move(bounds.X+130, bounds.Y+6)
		tt.Release(bounds.X+130, bounds.Y+6)
		if !tt.HasText("复制") {
			t.Fatal("a fresh selection did not reopen its menu")
		}
	}
	selectAll()
	if err := tt.Click("全选"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("复制") {
		t.Fatal("select all removed the copy action")
	}
	if err := tt.Click("复制"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "first selectable line\nsecond selectable line" {
		t.Fatalf("copied %q", tt.Clipboard())
	}
	assertClosed()
	if _, ok := term.SelectedText(); !ok {
		t.Fatal("dismissing the menu discarded the selection")
	}
	// After Copy closes the menu, an outside action still cancels its highlight.
	if err := tt.Click("其他操作"); err != nil {
		t.Fatal(err)
	}
	assertClosed()
	if _, ok := term.SelectedText(); ok {
		t.Fatal("outside action after Copy retained the selection highlight")
	}
	// A fresh gesture clears the previous selection before extending a new one.
	newSelection()
	if err := tt.Click("其他操作"); err != nil {
		t.Fatal(err)
	}
	if outsideClicks != 2 {
		t.Fatal("menu swallowed the outside action")
	}
	assertClosed()
	if _, ok := term.SelectedText(); ok {
		t.Fatal("outside action retained the selection highlight")
	}
	newSelection()
	tt.Key(0, ui.KeyEscape)
	assertClosed()
	if _, ok := term.SelectedText(); ok {
		t.Fatal("Escape retained the selection highlight")
	}
	newSelection()
	m.background = true
	tt.Frame()
	assertClosed()
	m.background = false
	assertClosed()
	newSelection()
}
