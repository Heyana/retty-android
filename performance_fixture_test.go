package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPerformanceFixtureRequiresOwnedDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perf.sock")
	if _, err := performanceFixtureToken(dir, path); err == nil {
		t.Fatal("unmarked application directory accepted")
	}
	token := strings.Repeat("a", 64)
	marker := `{"kind":"retty-performance-fixture","token":"` + token + `"}`
	if err := os.WriteFile(filepath.Join(dir, "perf-fixture.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := performanceFixtureToken(dir, path)
	if err != nil || got != token {
		t.Fatal("owned fixture rejected", err)
	}
	if _, err := performanceFixtureToken(dir, filepath.Join(t.TempDir(), "perf.sock")); err == nil {
		t.Fatal("socket outside fixture accepted")
	}
}

func TestPerformanceFixtureDisabledByDefault(t *testing.T) {
	t.Setenv("RETTY_PERF_SOCKET", "")
	if configurePerformanceFixture() {
		t.Fatal("ordinary startup enabled measurement mode")
	}
}
