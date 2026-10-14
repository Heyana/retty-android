//go:build retty_cli && (darwin || linux)

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"retty/internal/selfupdate"
)

func TestGatewayUpdateSurvivesRequestCancellationAndSerializes(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	entered, release, restarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	u := &gatewayUpdates{status: selfupdate.Status{Current: "1.1.0", Phase: "idle"}, restart: func() { close(restarted) }}
	u.check = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{Current: "1.1.0", Latest: "1.2.0", Available: true, Phase: "checked"}, nil
	}
	u.install = func(ctx context.Context, target string, progress func(string)) (string, error) {
		close(entered)
		<-release
		if ctx.Err() != nil {
			t.Error("request cancelled accepted job")
		}
		progress("installing")
		return "new/retty", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	status, err := u.request(ctx, selfupdate.Request{Action: "apply", Version: "1.2.0"})
	if err != nil || !status.Busy() {
		t.Fatal(status, err)
	}
	cancel()
	<-entered
	if _, err = u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "1.2.0"}); err == nil {
		t.Fatal("duplicate update started")
	}
	close(release)
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("gateway restart not scheduled")
	}
	status, err = u.request(context.Background(), selfupdate.Request{Action: "status"})
	if err != nil || status.Phase != "restarting" {
		t.Fatal(status, err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("RETTY_DIR"), "update.json"))
	if err != nil || len(data) == 0 {
		t.Fatal("reconnect receipt missing", err)
	}
}

func TestGatewayFailedUpdateNeverRestarts(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	done := make(chan struct{})
	u := &gatewayUpdates{status: selfupdate.Status{Current: "1.1.0"}, restart: func() { t.Error("failed update restarted") }}
	u.check = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{Current: "1.1.0", Latest: "1.2.0", Available: true}, nil
	}
	u.install = func(context.Context, string, func(string)) (string, error) {
		defer close(done)
		return "", errors.New("checksum mismatch")
	}
	if _, err := u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "1.3.0"}); err == nil {
		t.Fatal("unchecked version accepted")
	}
	if _, err := u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "1.2.0"}); err != nil {
		t.Fatal(err)
	}
	<-done
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := u.request(context.Background(), selfupdate.Request{Action: "status"})
		if status.Phase == "failed" {
			if status.Error != "checksum mismatch" {
				t.Fatal(status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGatewayUpdateRequiresWritableReceiptBeforeAccepting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RETTY_DIR", dir)
	if err := os.Mkdir(filepath.Join(dir, "update.json"), 0700); err != nil {
		t.Fatal(err)
	}
	u := &gatewayUpdates{restart: func() { t.Error("receipt failure restarted gateway") }}
	u.check = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{Current: "1.1.0", Latest: "1.2.0", Available: true}, nil
	}
	u.install = func(context.Context, string, func(string)) (string, error) {
		t.Error("receipt failure installed update")
		return "", nil
	}
	if _, err := u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "1.2.0"}); err == nil {
		t.Fatal("accepted update without durable receipt")
	}
}
