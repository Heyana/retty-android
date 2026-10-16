//go:build darwin || linux

package rex

import (
	"os"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPTYResizeAndForegroundDuringClose(t *testing.T) {
	for range 32 {
		master, slaveName, err := openMaster()
		if err != nil {
			t.Fatal(err)
		}
		slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			master.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { master.Close(); slave.Close() })
		if err := setSize(master, 100, 30); err != nil {
			master.Close()
			t.Fatal(err)
		}
		var size *unix.Winsize
		err = controlPTY(master, func(fd int) error {
			var err error
			size, err = unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
			return err
		})
		if err != nil || size.Col != 100 || size.Row != 30 {
			master.Close()
			t.Fatal("resize did not reach the PTY", size, err)
		}
		p := &pty{master: master}
		start := make(chan struct{})
		var workers sync.WaitGroup
		for worker := range 3 {
			workers.Go(func() {
				<-start
				if worker == 2 {
					master.Close()
					return
				}
				for range 32 {
					if worker == 0 {
						setSize(master, 80, 24)
					} else {
						p.foreground()
					}
				}
			})
		}
		close(start)
		workers.Wait()
		slave.Close()
		if err := setSize(master, 80, 24); err == nil {
			t.Fatal("resize of a closed PTY", err)
		}
		if pg := p.foreground(); pg != 0 {
			t.Fatal("closed PTY retained foreground process group", pg)
		}
	}
}
