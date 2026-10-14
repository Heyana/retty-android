//go:build darwin || linux

package rex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A disposable transport shaper; it models bandwidth, not packet loss,
// radio roaming, or Tailcat relay behavior. No real daemon is contacted.
type rateScreenConn struct {
	net.Conn
	rate int
	done chan struct{}
	once sync.Once
}

func (c *rateScreenConn) Close() error { c.once.Do(func() { close(c.done) }); return c.Conn.Close() }
func (c *rateScreenConn) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n := min(len(data), 4096)
		timer := time.NewTimer(time.Duration(float64(n) / float64(c.rate) * float64(time.Second)))
		select {
		case <-timer.C:
		case <-c.done:
			timer.Stop()
			return written, io.ErrClosedPipe
		}
		count, err := c.Conn.Write(data[:n])
		written += count
		if err != nil {
			return written, err
		}
		if count == 0 {
			return written, io.ErrNoProgress
		}
		data = data[count:]
	}
	return written, nil
}

type countedScreenConn struct {
	net.Conn
	received atomic.Int64
}

func (c *countedScreenConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	c.received.Add(int64(n))
	return n, err
}

func shapedViewer(t *testing.T, s *session, synced bool) (*Stream, *countedScreenConn) {
	t.Helper()
	server, client := net.Pipe()
	limited := &rateScreenConn{Conn: server, rate: 125000, done: make(chan struct{})}
	counted := &countedScreenConn{Conn: client}
	done := make(chan struct{})
	go func() {
		if synced {
			s.attachScreenSync(limited, bufio.NewReader(limited), 0, 0, false)
		} else {
			s.attachScreen(limited, 0, 0, true)
		}
		close(done)
	}()
	v := &Stream{conn: counted, screenFrames: true, screenSync: synced}
	v.cond = sync.NewCond(&v.mu)
	t.Cleanup(func() {
		v.Close()
		limited.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("shaped viewer leaked")
		}
	})
	return v, counted
}

type screenMeasurement struct {
	Scenario  string  `json:"scenario"`
	Mode      string  `json:"mode"`
	Sample    int     `json:"sample"`
	WireBytes int64   `json:"wire_bytes"`
	ElapsedMS float64 `json:"elapsed_ms"`
}

func TestScreenSyncBandwidthComparison(t *testing.T) {
	if os.Getenv("RETTY_SCREEN_SYNC_MEASURE") != "1" {
		t.Skip("opt-in isolated 1 Mbps comparison")
	}
	var results []screenMeasurement
	for _, synced := range []bool{false, true} {
		mode := "legacy"
		if synced {
			mode = "screen_sync"
		}
		for sample := 0; sample < 3; sample++ {
			s := stateFixture(t)
			var history strings.Builder
			for i := 0; i < 2000; i++ {
				fmt.Fprintf(&history, "history-%04d 中文\r\n", i)
			}
			s.vt.Feed([]byte(history.String()))
			v, counted := shapedViewer(t, s, synced)
			started := time.Now()
			data, _, _, err := v.ReadSnapshot()
			if err != nil || !bytes.Contains(data, []byte("history-1999")) {
				t.Fatalf("shaped first screen: %v", err)
			}
			results = append(results, screenMeasurement{"first_screen", mode, sample, counted.received.Load(), float64(time.Since(started)) / float64(time.Millisecond)})
			v.Close()
		}
		s := stateFixture(t)
		s.vt.Feed([]byte("initial"))
		v, counted := shapedViewer(t, s, synced)
		if _, _, _, err := v.ReadSnapshot(); err != nil {
			t.Fatal(err)
		}
		initial := counted.received.Load()
		started := time.Now()
		done := make(chan error, 1)
		go func() {
			buf := make([]byte, 64<<10)
			var tail []byte
			for {
				n, _, _, snapshot, err := v.ReadScreenUpdate(buf)
				if err != nil {
					done <- err
					return
				}
				if len(snapshot) > 0 {
					tail = snapshot
				} else {
					tail = append(tail, buf[:n]...)
					if len(tail) > 128<<10 {
						tail = tail[len(tail)-(128<<10):]
					}
				}
				if bytes.Contains(tail, []byte("FINAL_LATEST_STATE")) {
					done <- nil
					return
				}
			}
		}()
		for i := 0; i < 16; i++ {
			unit := []byte(fmt.Sprintf("\x1b[H\x1b[2Jprogress-%04d 中文🙂", i))
			unit = append(unit, bytes.Repeat([]byte(" "), 64-len(unit))...)
			block := bytes.Repeat(unit, 1024)
			s.mu.Lock()
			s.vt.Feed(block)
			s.output += uint64(len(block))
			s.broadcastLocked(block)
			s.mu.Unlock()
			until := started.Add(time.Duration(i+1) * time.Second / 16)
			if delay := time.Until(until); delay > 0 {
				time.Sleep(delay)
			}
		}
		final := []byte("\x1b[H\x1b[2JFINAL_LATEST_STATE")
		s.mu.Lock()
		s.vt.Feed(final)
		s.broadcastLocked(final)
		s.mu.Unlock()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("shaped screen never caught up")
		}
		results = append(results, screenMeasurement{"output_1MiB_per_second", mode, 0, counted.received.Load() - initial, float64(time.Since(started)) / float64(time.Millisecond)})
		v.Close()
	}
	data, _ := json.MarshalIndent(struct {
		BandwidthBPS int                 `json:"bandwidth_bps"`
		Samples      []screenMeasurement `json:"samples"`
	}{1000000, results}, "", "  ")
	t.Log(string(data))
	if path := os.Getenv("RETTY_SCREEN_SYNC_REPORT"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
