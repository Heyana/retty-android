// verifyupdates validates the MyGo update assets before publishing a Retty release.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var targets = []string{"darwin-universal", "darwin-arm64", "darwin-amd64"}
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

type config struct {
	Version string `json:"version"`
	Updates struct {
		GitHub    string `json:"github"`
		PublicKey string `json:"publicKey"`
	} `json:"updates"`
}

type asset struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

type manifest struct {
	Version string `json:"version"`
	asset
	Deltas []struct {
		From string `json:"from"`
		asset
	} `json:"deltas"`
}

func main() {
	dir := flag.String("dir", "build", "asset directory")
	flat := flag.Bool("flat", false, "assets are in one directory, without target subdirectories")
	list := flag.Bool("list", false, "print verified asset filenames, one per line")
	flag.Parse()
	var c config
	data, err := os.ReadFile("mygo.json")
	if err == nil {
		err = json.Unmarshal(data, &c)
	}
	var names []string
	if err == nil {
		names, err = verify(*dir, *flat, c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *list {
		for _, name := range names {
			fmt.Println(name)
		}
	} else {
		fmt.Printf("Verified %d signed macOS update assets\n", len(names))
	}
}

func verify(dir string, flat bool, c config) ([]string, error) {
	key, err := base64.StdEncoding.DecodeString(c.Updates.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid updates.publicKey")
	}
	if !versionPattern.MatchString(c.Version) || c.Updates.GitHub != "daodao97/retty" {
		return nil, fmt.Errorf("invalid Retty version or update repository")
	}
	prefix := "https://github.com/" + c.Updates.GitHub + "/releases/download/v" + c.Version + "/"
	var names []string
	for _, target := range targets {
		folder := dir
		if !flat {
			folder = filepath.Join(dir, target)
		}
		name := "update-" + target + ".json"
		data, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			return nil, err
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		if m.Version != c.Version {
			return nil, fmt.Errorf("%s: unexpected version %q", name, m.Version)
		}
		names = append(names, name)
		archive := "retty-" + c.Version + "-" + target + ".tar.gz"
		if err := verifyAsset(folder, archive, prefix, key, m.asset); err != nil {
			return nil, err
		}
		names = append(names, archive)
		seen := map[string]bool{}
		for _, delta := range m.Deltas {
			if !versionPattern.MatchString(delta.From) || delta.From == c.Version || seen[delta.From] {
				return nil, fmt.Errorf("%s: invalid or duplicate delta source %q", name, delta.From)
			}
			seen[delta.From] = true
			deltaName := "retty-" + delta.From + "-to-" + c.Version + "-" + target + ".delta"
			if err := verifyAsset(folder, deltaName, prefix, key, delta.asset); err != nil {
				return nil, err
			}
			names = append(names, deltaName)
		}
	}
	return names, nil
}

func verifyAsset(dir, name, prefix string, key ed25519.PublicKey, a asset) error {
	if a.URL != prefix+name || a.Size <= 0 || a.Size > 1<<30 {
		return fmt.Errorf("%s: invalid update URL or size", name)
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, a.Size+1))
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if n != a.Size || err != nil || !ed25519.Verify(key, h.Sum(nil), sig) {
		return fmt.Errorf("%s: update size or signature mismatch", name)
	}
	return nil
}
