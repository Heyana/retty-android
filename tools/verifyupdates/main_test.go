package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func fixture(t *testing.T, flat bool) (string, config) {
	t.Helper()
	dir := t.TempDir()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := config{Version: "1.2.3"}
	c.Updates.GitHub = "daodao97/retty"
	c.Updates.PublicKey = base64.StdEncoding.EncodeToString(pub)
	for _, target := range targets {
		folder := dir
		if !flat {
			folder = filepath.Join(dir, target)
		}
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		writeAsset := func(name string) asset {
			data := []byte("signed fixture for " + name)
			if err := os.WriteFile(filepath.Join(folder, name), data, 0600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			return asset{
				URL:       "https://github.com/daodao97/retty/releases/download/v1.2.3/" + name,
				Size:      int64(len(data)),
				Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, digest[:])),
			}
		}
		m := manifest{Version: c.Version, asset: writeAsset("retty-1.2.3-" + target + ".tar.gz")}
		m.Deltas = append(m.Deltas, struct {
			From string `json:"from"`
			asset
		}{
			From: "1.2.2", asset: writeAsset("retty-1.2.2-to-1.2.3-" + target + ".delta"),
		})
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "update-"+target+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, c
}

func TestVerifySignedUpdates(t *testing.T) {
	for _, flat := range []bool{false, true} {
		dir, c := fixture(t, flat)
		names, err := verify(dir, flat, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(names) != 9 || !slices.Contains(names, "retty-1.2.2-to-1.2.3-darwin-arm64.delta") {
			t.Fatalf("incomplete publish list: %v", names)
		}
	}
}

func TestVerifyRejectsBrokenRelease(t *testing.T) {
	for _, mutation := range []string{"tampered archive", "missing delta", "missing target", "wrong key", "wrong version", "wrong URL", "wrong size", "duplicate delta", "unsafe delta source"} {
		t.Run(mutation, func(t *testing.T) {
			dir, c := fixture(t, true)
			manifestPath := filepath.Join(dir, "update-darwin-arm64.json")
			data, _ := os.ReadFile(manifestPath)
			var m manifest
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "tampered archive":
				os.WriteFile(filepath.Join(dir, "retty-1.2.3-darwin-arm64.tar.gz"), make([]byte, m.Size), 0600)
			case "missing delta":
				os.Remove(filepath.Join(dir, "retty-1.2.2-to-1.2.3-darwin-arm64.delta"))
			case "missing target":
				os.Remove(manifestPath)
			case "wrong key":
				pub, _, _ := ed25519.GenerateKey(rand.Reader)
				c.Updates.PublicKey = base64.StdEncoding.EncodeToString(pub)
			case "wrong version":
				m.Version = "1.2.4"
			case "wrong URL":
				m.URL = "https://example.com/archive.tar.gz"
			case "wrong size":
				m.Size--
			case "duplicate delta":
				m.Deltas = append(m.Deltas, m.Deltas[0])
			case "unsafe delta source":
				m.Deltas[0].From = "../key"
			}
			if mutation != "missing target" {
				data, _ = json.Marshal(m)
				os.WriteFile(manifestPath, data, 0600)
			}
			if _, err := verify(dir, true, c); err == nil {
				t.Fatal("accepted broken update release")
			}
		})
	}
}
