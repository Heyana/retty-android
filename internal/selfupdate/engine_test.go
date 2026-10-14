//go:build retty_cli && (darwin || linux)

package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func testArchive(t *testing.T, entries []tar.Header) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		content := []byte("new-" + entry.Name)
		if entry.Typeflag == tar.TypeReg {
			entry.Size = int64(len(content))
		}
		if err := tw.WriteHeader(&entry); err != nil {
			t.Fatal(err)
		}
		if entry.Typeflag == tar.TypeReg {
			if _, err := tw.Write(content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func fixtureEngine(t *testing.T) (*Engine, *[]byte, *bool) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "retty")
	for name, content := range map[string]string{"retty": "old executable", "libghostty-vt.so": "old library"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	archive := testArchive(t, []tar.Header{{Name: "retty-linux-amd64/retty", Typeflag: tar.TypeReg}, {Name: "retty-linux-amd64/libghostty-vt.so", Typeflag: tar.TypeReg}})
	badHash := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.0", "assets": []map[string]string{{"name": "retty-linux-amd64.tar.gz"}, {"name": "retty-linux-amd64.tar.gz.sha256"}}})
		case "/v1.2.0/retty-linux-amd64.tar.gz":
			_, _ = w.Write(archive)
		case "/v1.2.0/retty-linux-amd64.tar.gz.sha256":
			sum := sha256.Sum256(archive)
			if badHash {
				sum[0] ^= 1
			}
			fmt.Fprintf(w, "%x  retty-linux-amd64.tar.gz\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	e, err := New("1.1.0", entry)
	if err != nil {
		t.Fatal(err)
	}
	e.Platform = "linux-amd64"
	e.api = server.URL + "/latest"
	e.download = server.URL + "/"
	e.client = server.Client()
	e.verify = func(context.Context, string, string) error { return nil }
	return e, &archive, &badHash
}

func TestAtomicUpdateKeepsPreviousBinaryAndLibrary(t *testing.T) {
	e, _, _ := fixtureEngine(t)
	old, err := os.Open(e.Entry)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	s, err := e.Check(context.Background())
	if err != nil || !s.Available || s.Latest != "1.2.0" {
		t.Fatal(s, err)
	}
	newExe, err := e.Install(context.Background(), s.Latest, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	active, err := filepath.EvalSymlinks(e.Entry)
	if err != nil || active != newExe {
		t.Fatal(active, err)
	}
	data, _ := io.ReadAll(old)
	if string(data) != "old executable" {
		t.Fatal("running executable was overwritten", string(data))
	}
	data, _ = os.ReadFile(filepath.Join(filepath.Dir(e.Entry), "libghostty-vt.so"))
	if string(data) != "old library" {
		t.Fatal("running library was overwritten")
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(e.Entry), ".retty-versions", "previous-*", "retty"))
	if len(backups) != 1 {
		t.Fatal("old installation not retained", backups)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(backups[0]), "libghostty-vt.so")); err != nil {
		t.Fatal(err)
	}
	next, err := New("1.2.0", newExe)
	if err != nil || next.Entry != e.Entry {
		t.Fatal("second update lost stable entry", next, err)
	}
	if _, err = e.Install(context.Background(), "1.0.0", func(string) {}); err == nil {
		t.Fatal("downgrade allowed")
	}
}

func TestUpdateFailurePreservesInstallation(t *testing.T) {
	for _, kind := range []string{"checksum", "runtime", "missing library", "traversal", "symlink", "duplicate", "changed version", "cancelled", "locked"} {
		t.Run(kind, func(t *testing.T) {
			e, archive, badHash := fixtureEngine(t)
			ctx := context.Background()
			target := "1.2.0"
			var lock *os.File
			switch kind {
			case "checksum":
				*badHash = true
			case "runtime":
				e.verify = func(context.Context, string, string) error { return errors.New("wrong architecture") }
			case "changed version":
				target = "1.3.0"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "locked":
				lock, _ = os.OpenFile(filepath.Join(filepath.Dir(e.Entry), ".retty-update.lock"), os.O_CREATE|os.O_RDWR, 0600)
				defer lock.Close()
				if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			default:
				entries := []tar.Header{{Name: "retty-linux-amd64/retty", Typeflag: tar.TypeReg}}
				switch kind {
				case "traversal":
					entries = append(entries, tar.Header{Name: "retty-linux-amd64/../../escape", Typeflag: tar.TypeReg})
				case "symlink":
					entries = append(entries, tar.Header{Name: "retty-linux-amd64/libghostty-vt.so", Typeflag: tar.TypeSymlink, Linkname: "/tmp/library"})
				case "duplicate":
					entries = append(entries, entries[0])
				}
				*archive = testArchive(t, entries)
			}
			if _, err := e.Install(ctx, target, func(string) {}); err == nil {
				t.Fatal("bad update succeeded")
			}
			data, _ := os.ReadFile(e.Entry)
			if string(data) != "old executable" {
				t.Fatal("failure replaced active CLI")
			}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(e.Entry), ".retty-versions", "v*"))
			if len(stages) != 0 {
				t.Fatal("failed stage leaked", stages)
			}
		})
	}
}

func TestStableVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		candidate, current string
		want               bool
	}{{"1.10.0", "1.9.9", true}, {"1.2.0", "1.2.0", false}, {"1.1.9", "1.2.0", false}, {"1.2.0-rc1", "1.1.0", false}, {"1.2.0", "dev", false}, {"01.2.0", "0.1.0", false}} {
		if newer(tc.candidate, tc.current) != tc.want {
			t.Fatal(tc)
		}
	}
	if _, ok := stableVersion("1.2.3/../../bad"); ok {
		t.Fatal("unsafe release path accepted")
	}
}

func TestRuntimeProbeUsesStagedCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "retty")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = '-verify-update' ] || exit 1\n[ -z \"$MYGO_GHOSTTY_VT\" ] || exit 1\nprintf '%s' '{\"version\":\"1.2.0\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYGO_GHOSTTY_VT", "unrelated library")
	if err := verifyPackage(context.Background(), binary, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(context.Background(), binary, "1.3.0"); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatal("wrong version passed", err)
	}
}
