package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"retty/internal/files"
	"retty/internal/rex"
	"testing"
	"time"
)

func TestBrowseFilesUsesBridgeWithoutDaemonOrInput(t *testing.T) {
	c, _, socket, _ := fileUploadFixture(t)
	// There is no daemon socket at all: browsing must be served by the bridge.
	// Removing this owned fixture socket leaves the fixture listener intact.
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.txt")
	os.WriteFile(path, []byte("artifact"), 0600)
	r, err := BrowseFiles(context.Background(), c, files.Request{Kind: "list", Path: dir})
	if err != nil || len(r.Entries) != 1 {
		t.Fatal(r, err)
	}
	r, err = BrowseFiles(context.Background(), c, files.Request{Kind: "read", Path: path, Length: 3})
	if err != nil || string(r.Data) != "art" || !r.Truncated {
		t.Fatal(r, err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
		r, err = BrowseFiles(context.Background(), c, files.Request{Kind: "changes", Path: dir})
		if err != nil || len(r.Entries) != 1 || r.Entries[0].Name != "artifact.txt" || r.Entries[0].Status != "??" {
			t.Fatal(r, err)
		}
	}

}

func TestBrowseFilesLegacyAndCancellation(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		control, peer := net.Pipe()
		defer control.Close()
		defer peer.Close()
		c := rex.NewClient(control, func(context.Context) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				var req files.Request
				json.NewDecoder(server).Decode(&req)
				if legacy {
					json.NewEncoder(server).Encode(rex.Response{Error: `unknown op "files-browse"`})
				} else {
					var b [1]byte
					server.Read(b[:])
				}
			}()
			return client, nil
		})
		defer c.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := BrowseFiles(ctx, c, files.Request{Kind: "list", Path: "/"})
		cancel()
		if legacy && !errors.Is(err, ErrFileBrowseUnsupported) {
			t.Fatal(err)
		}
		if !legacy && err == nil {
			t.Fatal("cancelled stream returned success")
		}
	}
}
