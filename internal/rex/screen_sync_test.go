//go:build darwin || linux

package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func stateFixture(t *testing.T) *session {
	t.Helper()
	screen, input, err := newSessionScreen(80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{vt: screen, cols: 80, rows: 24, clients: map[*attached]struct{}{}, done: make(chan struct{})}
	t.Cleanup(func() { input.Close(); screen.Close() })
	return s
}

func stateViewer(t *testing.T, s *session) *Stream {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { s.attachScreenSync(server, bufio.NewReader(server), 0, 0, false); close(done) }()
	stream := &Stream{conn: client, screenFrames: true, screenSync: true}
	stream.cond = sync.NewCond(&stream.mu)
	t.Cleanup(func() {
		stream.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("state attachment leaked")
		}
	})
	return stream
}

func TestScreenSyncFirstViewportAndIndependentHistory(t *testing.T) {
	s := stateFixture(t)
	var output strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&output, "history-%04d 中文\r\n", i)
	}
	s.vt.Feed([]byte(output.String()))
	viewer := stateViewer(t, s)
	first, cols, rows, err := viewer.ReadSnapshot()
	if err != nil || cols != 80 || rows != 24 {
		t.Fatalf("first screen: %dx%d %v", cols, rows, err)
	}
	if bytes.Contains(first, []byte("history-0000")) || !bytes.Contains(first, []byte("history-1999")) {
		t.Fatalf("viewport contained old history or lost current text: %q", first)
	}
	history, err := s.history(512)
	if err != nil || !bytes.Contains(history.Data, []byte("history-1500")) || bytes.Contains(history.Data, []byte("history-0000")) {
		t.Fatalf("history bounds: %v, %d bytes", err, len(history.Data))
	}
	if _, err := s.history(8193); err == nil {
		t.Fatal("unbounded history request accepted")
	}
	t.Logf("first viewport %d bytes, complete legacy snapshot %d bytes, 512-row history %d bytes", len(first), len(s.vt.Snapshot()), len(history.Data))
}

func TestScreenSyncSlowReaderReceivesLatestState(t *testing.T) {
	s := stateFixture(t)
	s.vt.Feed([]byte("\x1b[Hinitial"))
	viewer := stateViewer(t, s)
	if _, _, _, err := viewer.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	// Keep the first state unacknowledged while many screen changes occur.
	for i := 0; i < 1000; i++ {
		data := []byte(fmt.Sprintf("\x1b[H\x1b[2Jlatest-%04d 中文🙂", i))
		s.mu.Lock()
		s.vt.Feed(data)
		s.broadcastLocked(data)
		s.mu.Unlock()
	}
	s.mu.Lock()
	for p := range s.screenClients {
		if len(p.wake) > 1 {
			t.Fatal("output wake queue grew")
		}
	}
	s.mu.Unlock()
	_, cols, rows, next, err := viewer.ReadScreenUpdate(make([]byte, 64<<10))
	if err != nil || cols != 80 || rows != 24 || !bytes.Contains(next, []byte("latest-0999 中文🙂")) || bytes.Contains(next, []byte("latest-0001")) {
		t.Fatalf("latest state: %v %q", err, next)
	}
	if viewer.stateSequence != 2 {
		t.Fatalf("replayed intermediate frames: sequence %d", viewer.stateSequence)
	}
}

func TestScreenSyncHeartbeatConfirmsQuietScreen(t *testing.T) {
	s := stateFixture(t)
	s.vt.Feed([]byte("quiet"))
	viewer := stateViewer(t, s)
	if _, _, _, err := viewer.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	_, before, _ := viewer.ScreenStatus()
	done := make(chan struct{})
	go func() { viewer.ReadScreenUpdate(make([]byte, 4096)); close(done) }()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_, after, _ := viewer.ScreenStatus()
		if after.After(before) {
			viewer.Close()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	viewer.Close()
	<-done
	t.Fatal("quiet terminal did not confirm a heartbeat")
}

func TestScreenSyncInputPreservesLiteralBytes(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	ss, err := newSession("state-input", CreateOptions{Dir: t.TempDir(), Command: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	server := &Server{sessions: map[string]*session{ss.id: ss}}
	c := &Client{closed: make(chan struct{}), dial: func(context.Context) (net.Conn, error) { a, b := net.Pipe(); go server.handle(a); return b, nil }}
	v := c.ViewStream(ss.id).PreferScreenSync(false)
	t.Cleanup(func() { v.Close() })
	if err := v.Resize(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := v.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Write([]byte("printf 'STATE_INPUT_%s\\n' '中文🙂'\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, _, data, err := v.ReadScreenUpdate(make([]byte, 4096))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("STATE_INPUT_中文🙂")) {
			return
		}
	}
	t.Fatal("framed input did not reach the owned PTY")
}

func TestScreenSyncNegotiationFallsBackToLegacy(t *testing.T) {
	var requested Attach
	input := make(chan []byte, 1)
	c := &Client{closed: make(chan struct{}), dial: func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer a.Close()
			r := bufio.NewReader(a)
			line, _ := r.ReadBytes('\n')
			json.Unmarshal(line, &requested)
			a.Write([]byte("{\"ok\":true,\"screen_frames\":true}\n"))
			data := []byte("legacy full snapshot")
			header := make([]byte, 8)
			binary.BigEndian.PutUint32(header, uint32(len(data)))
			binary.BigEndian.PutUint16(header[4:], 80)
			binary.BigEndian.PutUint16(header[6:], 24)
			writeAll(a, append(header, data...))
			buf := make([]byte, 32)
			n, _ := a.Read(buf)
			input <- bytes.Clone(buf[:n])
		}()
		return b, nil
	}}
	v := c.ViewStream("legacy").PreferScreenSync(false)
	t.Cleanup(func() { v.Close() })
	if err := v.Resize(0, 0); err != nil {
		t.Fatal(err)
	}
	data, _, _, err := v.ReadSnapshot()
	if err != nil || string(data) != "legacy full snapshot" {
		t.Fatalf("legacy snapshot: %q %v", data, err)
	}
	if enabled, _, _ := v.ScreenStatus(); enabled || !requested.ScreenSync {
		t.Fatal("negotiation did not fall back")
	}
	v.Write([]byte("literal input"))
	if got := <-input; string(got) != "literal input" {
		t.Fatalf("legacy input was framed: %q", got)
	}
}

func TestScreenDifferencesReconstructUnicodeAndRejectInvalidBase(t *testing.T) {
	previous := []byte(strings.Repeat("prefix", 100) + "中文🙂" + strings.Repeat("suffix", 100))
	next := bytes.Replace(previous, []byte("中文🙂"), []byte("中文🚀"), 1)
	kind, delta := screenDifference(previous, next)
	if kind != screenDelta || len(delta) >= len(next) {
		t.Fatal("small change did not produce a delta")
	}
	got, err := applyScreenDifference(previous, delta)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("Unicode state corrupted: %v", err)
	}
	bad := make([]byte, 8)
	binary.BigEndian.PutUint32(bad, 0xffffffff)
	if _, err := applyScreenDifference(previous, bad); err == nil {
		t.Fatal("invalid base accepted")
	}
}

type shortStateWriter struct{ bytes.Buffer }

func (w *shortStateWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:min(len(p), 3)]) }
func TestScreenSyncHandlesShortWrites(t *testing.T) {
	var w shortStateWriter
	if err := writeAll(&w, []byte("complete packet")); err != nil || w.String() != "complete packet" {
		t.Fatalf("short writes: %q %v", w.String(), err)
	}
}

func TestClientExtensionsStayOnOriginalSocket(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	listener, err := net.Listen("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	first, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	t.Setenv("RETTY_DIR", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	extension, err := c.DialExtension(ctx)
	if err != nil {
		t.Fatalf("extension followed changed environment: %v", err)
	}
	extension.Close()
	second, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestScreenSyncWaitsForCompleteProgramRedraw(t *testing.T) {
	s := stateFixture(t)
	s.vt.Feed([]byte("OLD_FRAME"))
	v := stateViewer(t, s)
	if _, _, _, err := v.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	data := []byte("\x1b[?2026h\x1b[H\x1b[2JPARTIAL_FRAME")
	s.vt.Feed(data)
	s.broadcastLocked(data)
	s.mu.Unlock()
	result := make(chan []byte, 1)
	failure := make(chan error, 1)
	go func() {
		_, _, _, data, err := v.ReadScreenUpdate(make([]byte, 4096))
		if err != nil {
			failure <- err
		} else {
			result <- data
		}
	}()
	select {
	case data := <-result:
		t.Fatalf("uncommitted redraw leaked: %q", data)
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}
	s.mu.Lock()
	data = []byte("\x1b[H\x1b[2JCOMPLETE_FRAME\x1b[?2026l")
	s.vt.Feed(data)
	s.broadcastLocked(data)
	s.mu.Unlock()
	select {
	case data := <-result:
		if !bytes.Contains(data, []byte("COMPLETE_FRAME")) || bytes.Contains(data, []byte("PARTIAL_FRAME")) || bytes.Contains(data, []byte("\x1b[?2026h")) {
			t.Fatalf("invalid committed frame: %q", data)
		}
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("committed redraw not delivered")
	}
}

func TestScreenSyncInterruptsOwnedFloodingPTY(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	script := `stty -echo; trap 'printf STOPPED_BY_INPUT; exit 0' INT; while :; do printf 'busy-output\r'; done`
	ss, err := newSession("state-interrupt", CreateOptions{Dir: t.TempDir(), Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	server := &Server{sessions: map[string]*session{ss.id: ss}}
	c := &Client{closed: make(chan struct{}), dial: func(context.Context) (net.Conn, error) { a, b := net.Pipe(); go server.handle(a); return b, nil }}
	v := c.ViewStream(ss.id).PreferScreenSync(false)
	t.Cleanup(func() { v.Close() })
	if err := v.Resize(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := v.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := v.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ss.done:
		if time.Since(started) > time.Second {
			t.Fatal("input waited for output backlog")
		}
	case <-time.After(time.Second):
		t.Fatal("Ctrl+C did not stop owned output producer")
	}
	t.Logf("Ctrl+C reached owned flooding PTY in %s", time.Since(started))
}

func TestScreenSyncOneTimeEffectsSurviveSmallReads(t *testing.T) {
	s := stateFixture(t)
	s.vt.Feed([]byte("unchanged"))
	v := stateViewer(t, s)
	if _, _, _, err := v.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	event := []byte("\a\x1b]52;c;aGVsbG8=\x07\x1b]777;notify;title;body\x07")
	s.mu.Lock()
	s.sendScreenEffectLocked(event)
	s.mu.Unlock()
	var got []byte
	for len(got) < len(event) {
		buf := make([]byte, 1)
		n, _, _, snapshot, err := v.ReadScreenUpdate(buf)
		if err != nil || len(snapshot) > 0 || n != 1 {
			t.Fatalf("event delivery: n=%d snapshot=%d %v", n, len(snapshot), err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, event) || v.stateSequence != 2 {
		t.Fatalf("events duplicated or dropped: %q seq=%d", got, v.stateSequence)
	}
}

func TestScreenSyncAnswersTerminalQueriesWithoutByteViewer(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	script := `stty raw -echo; printf '\033[6n'; dd bs=1 count=6 2>/dev/null | od -An -tx1; printf 'QUERY_DONE'; sleep 2`
	ss, err := newSession("state-query", CreateOptions{Dir: t.TempDir(), Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	server := &Server{sessions: map[string]*session{ss.id: ss}}
	c := &Client{closed: make(chan struct{}), dial: func(context.Context) (net.Conn, error) { a, b := net.Pipe(); go server.handle(a); return b, nil }}
	v := c.ViewStream(ss.id).PreferScreenSync(false)
	t.Cleanup(func() { v.Close() })
	if err := v.Resize(0, 0); err != nil {
		t.Fatal(err)
	}
	data, _, _, err := v.ReadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for !bytes.Contains(data, []byte("QUERY_DONE")) {
		_, _, _, data, err = v.ReadScreenUpdate(make([]byte, 4096))
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(string(data)), " "), "1b 5b 31 3b 31 52") {
		t.Fatalf("cursor report was not answered: %q", data)
	}
}

func TestScreenSyncSnapshotsPreserveModesLinksAndResize(t *testing.T) {
	s := stateFixture(t)
	s.vt.Feed([]byte("\x1b]8;;https://example.com\x1b\\" + strings.Repeat("中文🙂", 40) + "\x1b]8;;\x1b\\\x1b[?2004h\x1b[?25l"))
	v := stateViewer(t, s)
	clone, closer, err := newSessionScreen(80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closer.Close(); clone.Close() })
	data, cols, rows, err := v.ReadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	clone.Resize(cols, rows)
	clone.Feed(data)
	assertScreenEquivalent(t, s.vt, clone)
	for _, alternate := range []bool{true, false} {
		s.mu.Lock()
		s.cols, s.rows = 100, 30
		s.vt.Resize(100, 30)
		mode := "\x1b[?1049l"
		if alternate {
			mode = "\x1b[?1049h"
		}
		delta := []byte(mode + "\x1b[H\x1b[2J新尺寸 🚀\x1b[?2004h")
		s.vt.Feed(delta)
		s.broadcastLocked(delta)
		s.mu.Unlock()
		_, cols, rows, data, err = v.ReadScreenUpdate(make([]byte, 4096))
		if err != nil {
			t.Fatal(err)
		}
		clone.Resize(cols, rows)
		// A state replacement starts from a fresh emulator, as native viewers do.
		fresh, cleanup, err := newSessionScreen(cols, rows, nil)
		if err != nil {
			t.Fatal(err)
		}
		fresh.Feed(data)
		assertScreenEquivalent(t, s.vt, fresh)
		cleanup.Close()
		fresh.Close()
	}
}

func assertScreenEquivalent(t *testing.T, source, clone sessionScreen) {
	t.Helper()
	if source.Text() != clone.Text() {
		t.Fatalf("screen text changed: source=%q clone=%q", source.Text(), clone.Text())
	}
	for _, screen := range []sessionScreen{source, clone} {
		if !bytes.Contains(screen.SnapshotRecent(-1), []byte("https://example.com")) && strings.Contains(screen.Text(), "中文🙂") {
			t.Fatal("cell hyperlink lost")
		}
	}
	// Query native state: equivalent hyperlink serializations need not be byte-identical.
	var responses [][][]byte
	for _, screen := range []sessionScreen{source, clone} {
		capture := screen.(interface{ DrainEffects() ([][]byte, [][]byte) })
		capture.DrainEffects()
		screen.Feed([]byte("\x1b[6n\x1b[?2004$p\x1b[?25$p"))
		replies, _ := capture.DrainEffects()
		responses = append(responses, replies)
	}
	if !reflect.DeepEqual(responses[0], responses[1]) {
		t.Fatalf("cursor or modes changed: source=%q clone=%q", responses[0], responses[1])
	}
}

func TestScreenSyncPreservesFinalExitedState(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	ss, err := newSession("state-final", CreateOptions{Dir: t.TempDir(), Command: []string{"/bin/sh", "-c", "printf FINAL_STATE; exit 7"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	select {
	case <-ss.done:
	case <-time.After(time.Second):
		t.Fatal("owned command did not exit")
	}
	v := stateViewer(t, ss)
	data, _, _, err := v.ReadSnapshot()
	if err != nil || !bytes.Contains(data, []byte("FINAL_STATE")) {
		t.Fatalf("final state lost: %q %v", data, err)
	}
}
