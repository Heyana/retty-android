//go:build retty_cli && (darwin || linux)

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"retty/internal/rex"
	"retty/internal/selfupdate"
)

// Uses a test-only download installer and transport, but the real management
// socket, updater lifecycle, cleanup and exec. The daemon and PTY are real.
func TestGatewayUpdateExecPreservesOwnedDaemonAndShell(t *testing.T) {
	if os.Getenv("RETTY_CLI_E2E") != "1" || os.Getenv("RETTY_CLI_BINARY") == "" {
		t.Skip("set RETTY_CLI_E2E and packaged RETTY_CLI_BINARY")
	}
	dir := t.TempDir()
	t.Setenv("RETTY_DIR", filepath.Join(dir, "state"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := exec.Command(os.Getenv("RETTY_CLI_BINARY"), "-server")
	server.Stdout, server.Stderr = io.Discard, io.Discard
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	client := waitLocal(t, ctx)
	t.Cleanup(func() { client.Shutdown(); client.Close() })
	hello, err := client.Hello()
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Create(rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}, Dir: dir, Cols: 93, Rows: 27})
	if err != nil {
		t.Fatal(err)
	}
	before, err := client.List()
	if err != nil || len(before) != 1 {
		t.Fatal(before, err)
	}
	binary := filepath.Join(dir, "retty")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = copyFixture(source, binary); err != nil {
		t.Fatal(err)
	}
	library := "libghostty-vt.so"
	if runtime.GOOS == "darwin" {
		library = "libghostty-vt.dylib"
	}
	if err = copyFixture(filepath.Join(filepath.Dir(os.Getenv("RETTY_CLI_BINARY")), library), filepath.Join(dir, library)); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestGatewayUpdateExecHelper$")
	cmd.Env = append(os.Environ(), "RETTY_UPDATE_EXEC_HELPER=1")
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })
	var initial gatewayState
	for {
		initial, err = gatewayRequest(ctx, "status")
		if err == nil && initial.Ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture gateway startup", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err = updateGatewayRequest(ctx, selfupdate.Request{Action: "apply", Version: "1.2.0"}); err != nil {
		t.Fatal(err)
	}
	var updated gatewayState
	for {
		updated, err = gatewayRequest(ctx, "update:{\"action\":\"status\"}")
		if err == nil && updated.Ready && updated.Update != nil && updated.Update.Phase == "updated" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture gateway update", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if updated.PID != initial.PID || updated.Update.Current != "1.2.0" {
		t.Fatal("exec changed supervisor PID or failed to load new version", initial.PID, updated)
	}
	after, err := client.List()
	newHello, helloErr := client.Hello()
	if err != nil || helloErr != nil || newHello.PID != hello.PID || len(after) != 1 || after[0].ID != session.ID || after[0].PID != before[0].PID || after[0].Cols != 93 || after[0].Rows != 27 || after[0].Exited {
		t.Fatal("gateway update altered PTY daemon or session", after, err, helloErr)
	}
	if err = stopGateway(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func copyFixture(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func TestGatewayUpdateExecHelper(t *testing.T) {
	if os.Getenv("RETTY_UPDATE_EXEC_HELPER") != "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	version = "1.1.0"
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if strings.Contains(exe, string(filepath.Separator)+".retty-versions"+string(filepath.Separator)) {
		version = "1.2.0"
	}
	gatewayUpdateFactory = func(restart func()) *gatewayUpdates {
		u := newGatewayUpdates(restart)
		u.check = func(context.Context) (selfupdate.Status, error) {
			return selfupdate.Status{Current: version, Latest: "1.2.0", Available: version != "1.2.0"}, nil
		}
		u.install = func(ctx context.Context, target string, progress func(string)) (string, error) {
			progress("installing")
			stage := filepath.Join(filepath.Dir(u.engine.Entry), ".retty-versions", "v1.2.0")
			if err := os.MkdirAll(stage, 0700); err != nil {
				return "", err
			}
			binary := filepath.Join(stage, "retty")
			if err := copyFixture(exe, binary); err != nil {
				return "", err
			}
			link := u.engine.Entry + ".next"
			if err := os.Symlink(binary, link); err != nil {
				return "", err
			}
			return binary, os.Rename(link, u.engine.Entry)
		}
		return u
	}
	gatewayServe = func(ctx context.Context, out, diagnostics io.Writer, linkFile string, qr bool, ready func(string), updates ...*gatewayUpdates) error {
		ready("fixture-only")
		<-ctx.Done()
		return nil
	}
	if err = runGateway(context.Background(), io.Discard, io.Discard, filepath.Join(rex.Dir(), "fixture-link"), false); err != nil {
		t.Fatal(err)
	}
}
