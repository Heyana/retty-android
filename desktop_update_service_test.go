package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"retty/internal/rex"
	"retty/internal/selfupdate"
)

func TestDesktopRemoteUpdateUsesNativeInstallerAndPreservesSession(t *testing.T) {
	a, _ := newTestApp(t)
	a.newLocalCommandTab(t.TempDir(), []string{"/bin/sh", "-c", "sleep 30"})
	before, err := a.client.List()
	if err != nil {
		t.Fatal(err)
	}
	hello, err := a.client.Hello()
	if err != nil {
		t.Fatal(err)
	}
	entered, proceed, relaunched := make(chan struct{}), make(chan struct{}), make(chan struct{})
	u := &desktopUpdateService{dir: rex.Dir(), status: selfupdate.Status{Kind: "desktop", Current: "1.0.0", Phase: "idle"}, relaunch: func() { close(relaunched) }}
	u.check = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{Kind: "desktop", Current: "1.0.0", Latest: "2.0.0", Available: true, Phase: "checked"}, nil
	}
	u.install = func(ctx context.Context, version string) error {
		if version != "2.0.0" {
			t.Error(version)
		}
		close(entered)
		<-proceed
		if ctx.Err() != nil {
			t.Error("request cancellation reached accepted native install")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	status, err := u.request(ctx, selfupdate.Request{Action: "apply", Version: "2.0.0"})
	if err != nil || status.Kind != "desktop" || !status.Busy() {
		t.Fatal(status, err)
	}
	cancel()
	<-entered
	if _, err = u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "2.0.0"}); err == nil {
		t.Fatal("duplicate native update accepted")
	}
	close(proceed)
	select {
	case <-relaunched:
	case <-time.After(time.Second):
		t.Fatal("GUI relaunch not scheduled")
	}
	status, _ = u.request(context.Background(), selfupdate.Request{Action: "status"})
	if status.Phase != "restarting" {
		t.Fatal(status)
	}
	data, err := os.ReadFile(filepath.Join(u.dir, "desktop-update.json"))
	var receipt selfupdate.Status
	if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Latest != "2.0.0" || receipt.Phase != "restarting" {
		t.Fatal("GUI relaunch receipt missing", err, string(data))
	}
	after, err := a.client.List()
	newHello, helloErr := a.client.Hello()
	if err != nil || helloErr != nil || newHello.PID != hello.PID || len(after) != len(before) {
		t.Fatal("desktop updater altered daemon or sessions", err, helloErr)
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].PID != before[i].PID || after[i].Cols != before[i].Cols || after[i].Rows != before[i].Rows || !after[i].LastInput.Equal(before[i].LastInput) || after[i].Exited {
			t.Fatal("desktop update changed PTY or input")
		}
	}
}

func TestDesktopRemoteUpdateRejectsChangedVersionAndFailedSignature(t *testing.T) {
	u := &desktopUpdateService{dir: t.TempDir(), relaunch: func() { t.Error("failed native installation relaunched") }}
	u.check = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{Kind: "desktop", Current: "1.0.0", Latest: "2.0.0", Available: true}, nil
	}
	u.install = func(context.Context, string) error { return errors.New("invalid signature") }
	if _, err := u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "3.0.0"}); err == nil {
		t.Fatal("unselected release installed")
	}
	if _, err := u.request(context.Background(), selfupdate.Request{Action: "apply", Version: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := u.request(context.Background(), selfupdate.Request{Action: "status"})
		if status.Phase == "failed" {
			if status.Error != "invalid signature" {
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
