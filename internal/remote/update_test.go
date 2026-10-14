package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"retty/internal/rex"
	"retty/internal/selfupdate"
)

func TestRemoteUpdateDoesNotReachSessionDaemon(t *testing.T) {
	var calls atomic.Int32
	b := &Bridge{imageContext: context.Background(), onUpdate: func(ctx context.Context, req selfupdate.Request) (selfupdate.Status, error) {
		calls.Add(1)
		if req.Op != "self-update" || req.Action != "apply" || req.Version != "1.2.0" {
			t.Error(req)
		}
		return selfupdate.Status{Current: "1.1.0", Latest: "1.2.0", Phase: "downloading"}, nil
	}}
	control, peer := net.Pipe()
	defer control.Close()
	defer peer.Close()
	c := rex.NewClient(control, func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() { defer server.Close(); b.serveConnection(server, "/nonexistent-disposable-daemon.sock") }()
		return client, nil
	})
	defer c.Close()
	status, err := Update(context.Background(), c, selfupdate.Request{Action: "apply", Version: "1.2.0"})
	if err != nil || status.Phase != "downloading" || calls.Load() != 1 {
		t.Fatal(status, err, calls.Load())
	}
}

func TestRemoteUpdateUnsupportedAndCancellation(t *testing.T) {
	for _, kind := range []string{"old gateway", "desktop gateway", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			control, peer := net.Pipe()
			defer control.Close()
			defer peer.Close()
			c := rex.NewClient(control, func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					if kind == "desktop gateway" {
						b := &Bridge{}
						b.serveConnection(server, "/unused.sock")
						return
					}
					var req selfupdate.Request
					json.NewDecoder(server).Decode(&req)
					if kind == "old gateway" {
						json.NewEncoder(server).Encode(rex.Response{Error: `unknown op "self-update"`})
					} else {
						var b [1]byte
						server.Read(b[:])
					}
				}()
				return client, nil
			})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err := Update(ctx, c, selfupdate.Request{Action: "check"})
			if kind == "cancelled" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrUpdateUnsupported) {
				t.Fatal(err)
			}
		})
	}
}
