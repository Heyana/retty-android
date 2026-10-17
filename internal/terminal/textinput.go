package terminal

import (
	"github.com/egoist/mygo/ui"
)

// textInput adapts an input method to keys the terminal sends.
//
// A terminal is not an editable text field, so it has no document to hand
// an input method: on iOS it was driven by UIKit's UITextInput protocol,
// which asks a view for text around a caret and gives it the result of a
// composition. mygo describes the same relationship with
// ui.TextInputClient, and Android's backend translates InputConnection
// calls into its methods — which is what opens the soft keyboard at all:
//
//	ui/input.go: only a state whose textClient is set reports
//	platform.TextInputState.Client, and internal/android/backend/textinput.go
//	raises the keyboard only when it has a client ("a TextInputState with no
//	client therefore raises no keyboard — this backend could not type into
//	it"). Before this file existed the terminal registered as focusable and
//	took HandleInput alone, so tapping it focused it and nothing else: the
//	keyboard stayed down and, with no keyboard, no key ever arrived.
//
// The text an input method composes and commits is not editing a buffer
// here — it is typing. #text is the committed text of the current
// composition, and every commit sends it as typed text, the same path
// view.InputText takes for a keyboard that sends characters. Nothing is
// stored: there is no document, so nothing can be queried, and an empty
// answer is the honest one.
//
// Main thread only, like the rest of the view.
type textInput struct {
	v *view

	// text is the composition in progress, which the input method shows as
	// its candidate bar. It is committed by ReplaceText or dropped by
	// UnmarkText. sel is the caret inside it, in UTF-16 code units, as
	// native input services count.
	text string
	sel  ui.TextInputRange

	// unmarked is the composition an input method ended without committing,
	// waiting for the commit that follows it.
	//
	// 🔴 An input method can end a composition and then commit only the
	// Return: Sogou does exactly that, and the log showed it as
	// MARKED "abc", UNMARK preedit="abc", REPLACE "\n". Read literally, that
	// discards the word the user typed and confirmed — the character loss
	// this type exists to undo. The platform contract has the same ordering
	// in mind ("the end-preedit/commit ordering", platform.InputComposition),
	// but it carries it to a client that owns a document: a terminal has no
	// document to keep the range in, so the text itself is kept here and
	// typed before the commit's own text.
	//
	// It is cleared by the commit that follows, and by any other edit: only
	// the one commit an unmark immediately precedes belongs to it.
	unmarked string
}

var _ ui.TextInputClient = (*textInput)(nil)

// client returns the terminal's text-input target, made on the first frame
// that needs one, so its address is stable for the element's lifetime.
func (v *view) client() *textInput {
	if v.ti == nil {
		v.ti = &textInput{v: v}
	}
	return v.ti
}

// TextForRange returns no document text: a terminal's scrollback is not a
// text field's content, and answering with it would let an input method
// propose to replace a program's output. The empty range says the same.
func (ti *textInput) TextForRange(r ui.TextInputRange) (string, ui.TextInputRange) {
	return "", ui.TextInputRange{}
}

// Selection reports the caret inside the composition. Outside one there is
// no position to report, so it collapses to zero — a committed character
// leaves the terminal's cursor where the program put it, not where a text
// field would leave a caret.
func (ti *textInput) Selection() ui.TextInputSelection {
	return ui.TextInputSelection{Range: ti.sel}
}

// MarkedRange reports the composition in progress, if there is one.
func (ti *textInput) MarkedRange() (ui.TextInputRange, bool) {
	if ti.text == "" {
		return ui.TextInputRange{}, false
	}
	return ui.TextInputRange{Start: 0, End: ui.UTF16Len(ti.text)}, true
}

// ReplaceText commits text — Android's commitText. A range means the method
// is replacing something it composed, and since the terminal keeps no
// document, what it replaces is the composition itself: the text sent is
// the new text, and what the old composition would have sent was never sent,
// because a composition is only drawn as candidates until now.
//
// This is the one path that types: everything an input method delivers —
// a completed character, a candidate, an emoji — arrives here.
func (ti *textInput) ReplaceText(r *ui.TextInputRange, text string) {
	ti.text, ti.sel = "", ui.TextInputRange{}
	// The composition an unmark ended goes out first: the input method
	// considered it typed when it ended it, and what arrives here is only
	// what it has to add after that text — Return, most often.
	if carry := ti.unmarked; carry != "" {
		ti.unmarked = ""
		ti.v.typed(carry)
	}
	if text != "" {
		ti.v.typed(text)
	}
	ti.v.t.redraw()
}

// SetMarkedText is Android's setComposingText: the candidate bar's current
// text. It is shown, not typed — typing it would send a letter per
// keystroke of a Chinese input method and then send the candidate too.
func (ti *textInput) SetMarkedText(r *ui.TextInputRange, text string, selected ui.TextInputRange) {
	// A composition starting is a new composition: whatever an unmark left
	// waiting belonged to the one before it, and no commit is coming for it.
	ti.unmarked = ""
	ti.text, ti.sel = text, selected
	ti.v.preedit, ti.v.preeditCaret = text, selected.Start
	ti.v.pending = nil
	ti.v.t.redraw()
}

// UnmarkText ends the composition. The text stays: it is what the user sees
// and what the input method considers typed.
//
// 🔴 This used to drop it, and that lost characters. Sogou sends Return as
// "finishComposingText, then commitText(\n)": the unmark ends the composition
// and the commit that follows carries only the newline. Dropping the preedit
// on unmark therefore discarded "abc" and sent just the Enter, so every word
// typed and then confirmed with Return vanished. The log said it plainly —
// MARKED "abc", UNMARK preedit="abc", REPLACE "\n".
//
// Keeping it is the right half of that pair: an unmark commits what was
// composed, and the text is typed. platform.InputComposition exists for this
// order — it remembers the range an unmark ended so a commit lands on it.
// An input method that only unmarks (an escape, a tap elsewhere) means "this
// is what I typed", and that is the same thing.
//
// The preedit is cleared because it is no longer provisional: the text is
// now in the program, not in the composition.
func (ti *textInput) UnmarkText() {
	// The text is handed to the commit that follows rather than typed now:
	// some input methods unmark and then commit the same text, and typing it
	// here would send it twice.
	ti.unmarked, ti.text, ti.sel = ti.v.preedit, "", ui.TextInputRange{}
	if ti.v.preedit != "" {
		ti.v.preedit = ""
		ti.v.t.redraw()
	}
}

// BoundsForRange answers where the composition starts: the terminal's cursor
// cell, which is what an input method anchors its candidate window to.
//
// The bool is true because the cell is a real place — it is where the
// program's cursor is, and where a candidate window belongs. Returning false
// would say "unavailable", and the engine then falls back to the element's
// whole box, which puts the candidate window at the top of a full-screen
// terminal instead of at the prompt (ui/input.go's updateTextInput does that
// fallback, and caret_test.go and links_test.go both caught it).
//
// The range reported is the composition's, clamped to what exists: a
// terminal has no document, so the only text with a measurable extent is the
// preedit this client is holding.
func (ti *textInput) BoundsForRange(r ui.TextInputRange) (ui.Rect, ui.TextInputRange, bool) {
	actual := ui.TextInputRange{Start: 0, End: ui.UTF16Len(ti.text)}
	return ti.v.caret(), actual, true
}

// IndexForPoint answers which character a touch lands on. A terminal has no
// characters in a document — the grid belongs to the program — so there is
// no offset to give, and the input method must not try to move a caret into
// output.
func (ti *textInput) IndexForPoint(p ui.Point) (int, bool) {
	return 0, false
}
