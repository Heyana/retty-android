//go:build darwin || linux

package files

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSpecialFilesNeverBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"read", "list"} {
		done := make(chan error, 1)
		go func() { _, err := Do(context.Background(), Request{Kind: kind, Path: path, Length: 1}); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("FIFO accepted", kind)
			}
		case <-time.After(time.Second):
			t.Fatal("FIFO blocked", kind)
		}
	}
}
