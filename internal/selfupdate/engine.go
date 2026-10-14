//go:build retty_cli && (darwin || linux)

// Package selfupdate installs official CLI releases without touching sessions.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const repository = "daodao97/retty"
const maxArchive = 128 << 20

type Engine struct {
	Current, Entry, Platform string
	client                   *http.Client
	api                      string
	download                 string
	verify                   func(context.Context, string, string) error
}

func New(current, executable string) (*Engine, error) {
	real, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(real)
	if filepath.Base(filepath.Dir(dir)) == ".retty-versions" {
		dir = filepath.Dir(filepath.Dir(dir))
	}
	entry := filepath.Join(dir, "retty")
	active, err := filepath.EvalSymlinks(entry)
	if err != nil || active != real {
		return nil, errors.New("update requires a packaged CLI named retty")
	}
	return &Engine{Current: current, Entry: entry, Platform: runtime.GOOS + "-" + runtime.GOARCH,
		verify: verifyPackage,
		client: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 10 {
				return errors.New("unsafe release redirect")
			}
			return nil
		}}, api: "https://api.github.com/repos/" + repository + "/releases/latest",
		download: "https://github.com/" + repository + "/releases/download/"}, nil
}

func stableVersion(v string) ([3]uint64, bool) {
	var out [3]uint64
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 1 && p[0] == '0' {
			return out, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return out, false
			}
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func newer(candidate, current string) bool {
	a, ok := stableVersion(candidate)
	if !ok {
		return false
	}
	b, ok := stableVersion(current)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (e *Engine) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Retty CLI updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("release response too large")
	}
	return data, err
}

func (e *Engine) Check(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status := Status{Current: e.Current, Phase: "checked"}
	if _, ok := stableVersion(e.Current); !ok {
		return status, errors.New("development/prerelease CLI builds require a manual update")
	}
	data, err := e.get(ctx, e.api, 2<<20)
	if err != nil {
		return status, err
	}
	var release struct {
		Tag               string `json:"tag_name"`
		Draft, Prerelease bool
		Assets            []struct{ Name string }
	}
	if err = json.Unmarshal(data, &release); err != nil {
		return status, err
	}
	if _, ok := stableVersion(release.Tag); !ok || !strings.HasPrefix(release.Tag, "v") || release.Draft || release.Prerelease {
		return status, errors.New("no valid stable CLI release")
	}
	name := "retty-" + e.Platform + ".tar.gz"
	hasArchive, hasHash := false, false
	for _, asset := range release.Assets {
		hasArchive = hasArchive || asset.Name == name
		hasHash = hasHash || asset.Name == name+".sha256"
	}
	if !hasArchive || !hasHash {
		return status, errors.New("latest release has no CLI package and checksum for this platform")
	}
	status.Latest = strings.TrimPrefix(release.Tag, "v")
	status.Available = newer(status.Latest, e.Current)
	return status, nil
}

// Install stages a complete binary/library pair, then atomically changes only
// the stable entry symlink. Old files remain for daemons and helpers using them.
func (e *Engine) Install(ctx context.Context, target string, progress func(string)) (string, error) {
	if !newer(target, e.Current) {
		return "", errors.New("update target must be a newer stable version")
	}
	dir := filepath.Dir(e.Entry)
	lock, err := os.OpenFile(filepath.Join(dir, ".retty-update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return "", errors.New("another CLI update is running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	status, err := e.Check(ctx)
	if err != nil {
		return "", err
	}
	if status.Latest != target || !status.Available {
		return "", errors.New("release changed; check updates again")
	}
	progress("downloading")
	name := "retty-" + e.Platform + ".tar.gz"
	base := e.download + "v" + target + "/"
	checksum, err := e.get(ctx, base+name+".sha256", 4096)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) != 2 || fields[1] != name {
		return "", errors.New("invalid release checksum")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil || len(expected) != sha256.Size {
		return "", errors.New("invalid SHA-256")
	}
	archive, err := e.get(ctx, base+name, maxArchive)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != strings.ToLower(fields[0]) {
		return "", errors.New("CLI package SHA-256 mismatch")
	}
	progress("installing")
	versions := filepath.Join(dir, ".retty-versions")
	if err = os.MkdirAll(versions, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(versions, "v"+target+"-")
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(stage)
		}
	}()
	if err = extract(archive, stage, e.Platform); err != nil {
		return "", err
	}
	if err = e.verify(ctx, filepath.Join(stage, "retty"), target); err != nil {
		return "", fmt.Errorf("new CLI runtime verification failed: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Preserve the original executable before replacing its directory entry.
	old, err := os.Lstat(e.Entry)
	if err != nil {
		return "", err
	}
	if old.Mode().IsRegular() {
		backup, err := os.MkdirTemp(versions, "previous-")
		if err != nil {
			return "", err
		}
		if err = os.Link(e.Entry, filepath.Join(backup, "retty")); err != nil {
			return "", err
		}
		library := "libghostty-vt.so"
		if strings.HasPrefix(e.Platform, "darwin-") {
			library = "libghostty-vt.dylib"
		}
		if err = os.Link(filepath.Join(dir, library), filepath.Join(backup, library)); err != nil {
			return "", err
		}
	}
	if !old.Mode().IsRegular() && old.Mode()&os.ModeSymlink == 0 {
		return "", errors.New("CLI entry is not a regular file or symlink")
	}
	tmp, err := os.CreateTemp(dir, ".retty-next-")
	if err != nil {
		return "", err
	}
	link := tmp.Name()
	tmp.Close()
	os.Remove(link)
	defer os.Remove(link)
	newExe := filepath.Join(stage, "retty")
	rel, err := filepath.Rel(dir, newExe)
	if err != nil {
		return "", err
	}
	if err = os.Symlink(rel, link); err != nil {
		return "", err
	}
	if err = os.Rename(link, e.Entry); err != nil {
		return "", err
	}
	committed = true
	return newExe, nil
}

func extract(data []byte, destination, platform string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	prefix := "retty-" + platform + "/"
	library := "libghostty-vt.so"
	if strings.HasPrefix(platform, "darwin-") {
		library = "libghostty-vt.dylib"
	}
	seen := map[string]bool{}
	var total int64
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeDir && strings.TrimSuffix(h.Name, "/") == strings.TrimSuffix(prefix, "/") {
			continue
		}
		name := strings.TrimPrefix(h.Name, prefix)
		if h.Name == name || name != "retty" && name != library && name != "README.md" || h.Typeflag != tar.TypeReg || seen[name] || h.Size <= 0 {
			return errors.New("unexpected CLI archive entry")
		}
		total += h.Size
		if total > 256<<20 {
			return errors.New("CLI archive expands beyond limit")
		}
		f, err := os.OpenFile(filepath.Join(destination, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, reader, h.Size)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		seen[name] = true
	}
	if !seen["retty"] || !seen[library] {
		return errors.New("CLI archive is missing executable or VT library")
	}
	return nil
}

func verifyPackage(ctx context.Context, binary, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-verify-update")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "MYGO_GHOSTTY_VT=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	data, err := cmd.Output()
	if err != nil {
		return err
	}
	var result struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &result) != nil || result.Version != target {
		return errors.New("package version does not match release")
	}
	return nil
}
