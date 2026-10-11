package rex

import (
	"encoding/json"
	"net"
	"testing"
)

func TestListOmitsUnchangedSessions(t *testing.T) {
	s := &Server{sessions: map[string]*session{}, quit: make(chan struct{})}
	first := s.request(Request{Op: "list"})
	if first.ETag == "" || string(first.Data) != "[]" {
		t.Fatalf("first list: %+v", first)
	}
	again := s.request(Request{Op: "list", ETag: first.ETag})
	if again.ETag != first.ETag || len(again.Data) != 0 {
		t.Fatalf("unchanged list was resent: %+v", again)
	}
	stale := s.request(Request{Op: "list", ETag: "stale"})
	if stale.ETag != first.ETag || string(stale.Data) != "[]" {
		t.Fatalf("a stale client did not get the list: %+v", stale)
	}
}

func TestClientReusesUnchangedList(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	c := NewClient(conn, nil)
	defer c.Close()
	sent := make(chan string, 3)
	go func() {
		decoder, encoder := json.NewDecoder(peer), json.NewEncoder(peer)
		for i := 0; ; i++ {
			var req Request
			if decoder.Decode(&req) != nil {
				return
			}
			sent <- req.ETag
			res := Response{ID: req.ID, ETag: "v1"}
			switch i {
			case 0:
				res.Data, _ = json.Marshal([]SessionInfo{{ID: "a"}})
			case 2:
				// An older server: no ETag, the whole list.
				res.ETag = ""
				res.Data, _ = json.Marshal([]SessionInfo{{ID: "b"}})
			}
			if encoder.Encode(res) != nil {
				return
			}
		}
	}()
	for i, want := range []struct{ etag, id string }{{"", "a"}, {"v1", "a"}, {"v1", "b"}} {
		list, err := c.List()
		if err != nil || len(list) != 1 || list[0].ID != want.id {
			t.Fatalf("list %d: %+v %v", i, list, err)
		}
		if etag := <-sent; etag != want.etag {
			t.Fatalf("list %d sent ETag %q, want %q", i, etag, want.etag)
		}
		list[0].ID = "mutated" // Callers own their copy.
	}
}
