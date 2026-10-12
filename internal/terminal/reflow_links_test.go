package terminal

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLinkHitTestingUsesReflowedDisplay(t *testing.T) {
	loadLib(t)
	for _, osc := range []bool{false, true} {
		var opened []Link
		term, err := New(Options{Conn: newPipe(), FixedCols: 80, FixedRows: 20, ReflowView: true, OpenLinksOnTap: true, OnOpenLink: func(link Link) { opened = append(opened, link) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { term.Close() })
		prefix := strings.Repeat("padding ", 8)
		url := "https://example.com/" + strings.Repeat("a", 35)
		text := url
		if osc {
			text = "\x1b]8;;" + url + "\x1b\\open link\x1b]8;;\x1b\\"
		}
		term.Feed([]byte(prefix + text))
		tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill().AutoFocus() }, 260, 200)
		col, row := (len(prefix)+2)%term.v.cols, (len(prefix)+2)/term.v.cols
		x, y := cellCenter(term, col, row)
		tt.ClickAt(x, y)
		if len(opened) != 1 || opened[0].URL != url {
			t.Fatalf("reflowed OSC=%v URL at %d,%d = %+v", osc, col, row, opened)
		}
	}
}
