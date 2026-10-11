package terminal

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestClearSelectionPreservesScreenClipboardAndRemoteInput(t *testing.T) {
	loadLib(t)
	for _, reflow := range []bool{false, true} {
		conn := newPipe()
		term, err := New(Options{Conn: conn, FixedCols: 80, FixedRows: 20, ReflowView: reflow})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { term.Close() })
		term.Feed([]byte("\x1b[?1002h\x1b[?1006hhello world"))
		initialized := false
		tt := ui.NewTester(func(c *ui.Context) {
			if !initialized {
				c.WriteClipboard("keep this clipboard")
				initialized = true
			}
			View(c, term).Fill()
		}, 393, 300)
		term.SelectAll()
		if _, ok := term.SelectedText(); !ok {
			t.Fatal("fixture has no selection")
		}
		term.Feed([]byte("\r\nnew output"))
		before := term.Snapshot()
		term.ClearSelection()
		tt.Frame()
		if _, ok := term.SelectedText(); ok {
			t.Fatal("selection retained after clearing")
		}
		if _, ok := term.SelectionAnchor(); ok {
			t.Fatal("selection menu anchor retained after clearing")
		}
		if !bytes.Equal(term.Snapshot(), before) || tt.Clipboard() != "keep this clipboard" {
			t.Fatal("clearing changed terminal contents or clipboard")
		}
		if reflow && !strings.Contains(term.presentation.Text(), "new output") {
			t.Fatal("clearing did not resume live output")
		}
		conn.mu.Lock()
		sent := string(conn.sent)
		conn.mu.Unlock()
		if sent != "" {
			t.Fatalf("clearing sent remote input: %q", sent)
		}
		term.ClearSelection()
		term.Close()
		term.ClearSelection()
	}
}
