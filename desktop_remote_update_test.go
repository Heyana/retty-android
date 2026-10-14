package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"retty/internal/rex"
	"retty/internal/selfupdate"
)

func TestRemoteUpdateEntryAndModalProtectSession(t *testing.T) {
	if !slices.Contains(paletteCommands, &cmdUpdateRemote) {
		t.Fatal("remote update missing from palette")
	}
	a, _ := newStaticTestApp(t)
	original := a.tab()
	a.openRemoteUpdate()
	if a.err == "" || a.remoteUpdate != nil {
		t.Fatal("local pane treated as a remote gateway")
	}
	d := &remoteUpdateDialog{open: true, host: &desktopHost{hello: rex.Hello{Host: rex.HostInfo{Name: "Fixture server"}}}, status: selfupdate.Status{Current: "1.1.0", Latest: "1.2.0", Available: true, Phase: "checked"}}
	a.remoteUpdate = d
	a.closeFocused()
	if d.open || a.tab() != original || original.Focus.closed {
		t.Fatal("close pane did not dismiss update modal")
	}
	d.open = true
	a.closeActiveTab()
	if d.open || a.tab() != original || original.Focus.closed {
		t.Fatal("close tab did not dismiss update modal")
	}
	d.open = true
	registerFonts()
	tt := ui.NewTester(func(c *ui.Context) { a.remoteUpdateView(c, colorsOf(c)) }, 520, 420)
	tt.Frame()
	if !tt.HasText("更新到 1.2.0") || !tt.HasText("Fixture server") {
		t.Fatal(tt.Texts())
	}
	saveSettingsImage(t, tt, "remote-update")
	d.status.Kind = "desktop"
	tt.Frame()
	if !tt.HasText("更新后会重开远端 Retty 窗口。后台会话服务和正在运行的任务保持不变。") {
		t.Fatal(tt.Texts())
	}
}
