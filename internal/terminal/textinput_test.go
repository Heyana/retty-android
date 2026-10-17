package terminal

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTextInputTypesWhatAnInputMethodCommits covers the client that makes
// the terminal reachable from a software keyboard: View registers it through
// HandleTextInput, and everything an input method commits is typed.
//
// Why this exists at all: registering as focusable and taking HandleInput is
// not enough to raise a keyboard on Android — the platform raises one only for
// a state that has a TextInputClient ("a TextInputState with no client
// therefore raises no keyboard", internal/android/backend/textinput.go). The
// terminal had none, so tapping it focused it and nothing else: no keyboard,
// no keys, nothing typed. These assertions pin the client that fixed that.
func TestTextInputTypesWhatAnInputMethodCommits(t *testing.T) {
	conn := newPipe()
	term, err := New(Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	v := term.viewOf(nil)
	// viewOf needs a context for its services; the client itself only touches
	// typed() and the preedit fields, which need no frame.
	ti := v.client()

	// A committed character is typed, through the same path a keyboard that
	// sends text takes.
	ti.ReplaceText(nil, "a")
	if got := conn.take(1); got != "a" {
		t.Fatalf("committed %q, want %q", got, "a")
	}

	// A composition is not typed until it commits: a Chinese input method
	// calls SetComposingText once per keystroke, and typing those would send
	// the preedit and then the candidate as well.
	ti.SetMarkedText(nil, "nihao", ui.TextInputRange{Start: 5, End: 5})
	if got := conn.take(1); got != "" {
		t.Fatalf("a composition typed %q, want nothing", got)
	}
	if marked, ok := ti.MarkedRange(); !ok || marked.Start != 0 || marked.End != 5 {
		t.Fatalf("MarkedRange() = %v, %v, want {0 5}, true", marked, ok)
	}
	if pre := v.preedit; pre != "nihao" {
		t.Errorf("preedit = %q, want %q", pre, "nihao")
	}

	// Committing it types the whole thing at once.
	ti.ReplaceText(nil, "你好")
	if got := conn.take(1); got != "你好" {
		t.Fatalf("committed %q, want %q", got, "你好")
	}
	if v.preedit != "" {
		t.Errorf("preedit = %q after commit, want it cleared", v.preedit)
	}
}

// TestTextInputNewlineIsEnter covers the Return key of a software keyboard,
// which arrives as committed text rather than as a key: an input method has
// no KeyEvent for it.
//
// It must become Enter, not a literal line feed. A shell at a prompt reads CR;
// a full-screen program that receives a bare LF reads it as Control+J. Sent as
// text, Return would do neither, which is why the terminal was unable to run a
// command even once characters got through.
func TestTextInputNewlineIsEnter(t *testing.T) {
	conn := newPipe()
	term, err := New(Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	ti := term.viewOf(nil).client()

	ti.ReplaceText(nil, "\n")
	if got := conn.take(1); got != "\r" {
		t.Fatalf("a committed newline sent %q, want %q (Enter is CR)", got, "\r")
	}

	// A newline inside a longer commit is text the program asked for — a pasted
	// block, a finished composition — and stays part of it.
	ti.ReplaceText(nil, "echo hi\n")
	if got := conn.take(1); got != "echo hi\n" {
		t.Fatalf("multi-line commit sent %q, want it unchanged", got)
	}
}

// TestCommittedTextIsSentWhole covers the input loss an input method showed:
// one commit of several characters reached the program as one character.
//
// The cause was the keystroke the terminal inferred for committed text. A
// one-character commit was mapped back to the key that would type it and sent
// through the encoder, which is right when a key and its text are one act and
// wrong for an input method, which commits text with no key at all — a commit
// of "abcde" went out as a single key press carrying "a". What an input
// method commits is text, so it is sent as text.
func TestCommittedTextIsSentWhole(t *testing.T) {
	conn := newPipe()
	term, err := New(Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	ti := term.viewOf(nil).client()

	for _, s := range []string{"a", "abcde", "你好", "x y"} {
		ti.ReplaceText(nil, s)
		if got := conn.take(len(s)); got != s {
			t.Errorf("committed %q sent %q", s, got)
		}
	}
}

// TestUnmarkedCompositionIsKeptForTheCommit covers the character loss Sogou
// showed on a real phone: a word typed and confirmed with Return vanished.
//
// The input method ends the composition and then commits only the newline —
// the log read MARKED "abc", UNMARK preedit="abc", REPLACE "\n". Read
// literally that discards the word the user typed and confirmed. The client
// keeps it and types it before the commit's own text, so the word and the
// Return both reach the program.
func TestUnmarkedCompositionIsKeptForTheCommit(t *testing.T) {
	conn := newPipe()
	term, err := New(Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	ti := term.viewOf(nil).client()

	ti.SetMarkedText(nil, "abc", ui.TextInputRange{Start: 3, End: 3})
	if got := conn.take(1); got != "" {
		t.Fatalf("a composition typed %q, want nothing", got)
	}
	ti.UnmarkText()
	if got := conn.take(1); got != "" {
		t.Fatalf("unmarking typed %q on its own, want nothing until the commit", got)
	}
	ti.ReplaceText(nil, "\n")
	if got := conn.take(4); got != "abc\r" {
		t.Fatalf("unmark then Return sent %q, want %q", got, "abc\r")
	}
}

// TestNewCompositionDropsAStaleUnmark keeps a cancelled composition from
// leaking into the next one: an unmark with no commit after it (Escape, a tap
// elsewhere) belongs to a composition that is over.
func TestNewCompositionDropsAStaleUnmark(t *testing.T) {
	conn := newPipe()
	term, err := New(Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	ti := term.viewOf(nil).client()

	ti.SetMarkedText(nil, "abc", ui.TextInputRange{Start: 3, End: 3})
	ti.UnmarkText()
	ti.SetMarkedText(nil, "xyz", ui.TextInputRange{Start: 3, End: 3})
	ti.ReplaceText(nil, "xyz")
	if got := conn.take(3); got != "xyz" {
		t.Fatalf("committed %q, want %q — the cancelled abc leaked in", got, "xyz")
	}
}
