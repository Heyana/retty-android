//go:build (darwin || linux) && !retty_cli

package rex

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"retty/internal/terminal"
)

func TestZshRestoresKeyboardAfterTUI(t *testing.T) {
	s := keyboardShell(t, "emacs")
	checkKey := func(want string) {
		t.Helper()
		client, peer := net.Pipe()
		defer peer.Close()
		term, err := terminal.New(terminal.Options{Conn: client})
		if err != nil {
			t.Fatal(err)
		}
		defer term.Close()
		term.Feed(s.vt.Snapshot())
		if !term.SendKey(ui.KeyC, ui.Ctrl) {
			t.Fatal("Ctrl+C was not sent")
		}
		peer.SetReadDeadline(time.Now().Add(time.Second))
		got := make([]byte, len(want))
		if _, err := io.ReadFull(peer, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("Ctrl+C = %q, want %q", got, want)
		}
		peer.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
		if n, _ := peer.Read(got); n != 0 {
			t.Fatalf("unexpected key release after Ctrl+C: %q", got[:n])
		}
	}
	checkKey("\x03")
	// Simulate a TUI that pushes nested keyboard modes and exits without
	// popping them. While it runs, its requested press/release mode works.
	s.input([]byte("printf '\\033[>1u\\033[>3uTUI-ACTIVE\\n'; read -r fixture; printf 'TUI-EXIT\\n'\r"))
	waitKeyboardShell(t, s, func(text string) bool { return strings.HasSuffix(text, "\nTUI-ACTIVE") })
	checkKey("\x1b[99;5u\x1b[99;5:3u")
	s.input([]byte("\r"))
	waitKeyboardShell(t, s, func(text string) bool { return strings.Count(text, "keyboard>") >= 2 })
	checkKey("\x03")
	// A subsequent TUI must start with a clean stack as well. Pop once
	// more while the command is still running so prompt cleanup cannot
	// mask a stale entry underneath the current flags.
	s.input([]byte("printf '\\033[>3u\\033[<u\\033[<uKEYBOARD-CLEAN\\n'; read -r fixture\r"))
	waitKeyboardShell(t, s, func(text string) bool { return strings.HasSuffix(text, "\nKEYBOARD-CLEAN") })
	checkKey("\x03")
	s.input([]byte("\r"))
	waitKeyboardShell(t, s, func(text string) bool { return strings.Count(text, "keyboard>") >= 3 })
}
