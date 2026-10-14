package sshconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeConfig(t *testing.T, home, name, text string) {
	t.Helper()
	path := filepath.Join(home, ".ssh", name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHostsAliasesAndIncludes(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "config", `
# Host commented-out
Host * !excluded *.example dev-?
  HostName ignored.example
  User ignored
hOsT = alpha beta "quoted" 'single' alpha # comment
Host -oProxyCommand=bad "not a host" !negated
Match exec "touch should-never-run"
  Include "conf.d/*.conf" "~/extra ssh.conf" missing.conf
Host final
`)
	writeConfig(t, home, "conf.d/02.conf", "Host second beta\nInclude config\n")
	writeConfig(t, home, "conf.d/01.conf", "Host first\nInclude nested.conf\n")
	writeConfig(t, home, "nested.conf", "Host nested\n")
	if err := os.WriteFile(filepath.Join(home, "extra ssh.conf"), []byte("Host extra\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hosts, err := Hosts(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "quoted", "single", "first", "nested", "second", "extra", "final"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("hosts = %q, want %q", hosts, want)
	}
}

func TestHostsMissingAndEditedConfig(t *testing.T) {
	home := t.TempDir()
	if hosts, err := Hosts(home); err != nil || len(hosts) != 0 {
		t.Fatalf("missing config: %q, %v", hosts, err)
	}
	writeConfig(t, home, "config", "Host one\n")
	if hosts, err := Hosts(home); err != nil || !reflect.DeepEqual(hosts, []string{"one"}) {
		t.Fatalf("first read: %q, %v", hosts, err)
	}
	writeConfig(t, home, "config", "Host two\n")
	if hosts, err := Hosts(home); err != nil || !reflect.DeepEqual(hosts, []string{"two"}) {
		t.Fatalf("edited config: %q, %v", hosts, err)
	}
}

func TestHostsReportsInvalidInclude(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "config", "Host before\nInclude \"unfinished\n")
	hosts, err := Hosts(home)
	if err == nil || !reflect.DeepEqual(hosts, []string{"before"}) {
		t.Fatalf("partial result: %q, %v", hosts, err)
	}
}

func TestHostsBoundsIncludeRecursion(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "config", "Include conf0\n")
	for i := 0; i < 18; i++ {
		writeConfig(t, home, fmt.Sprintf("conf%d", i), fmt.Sprintf("Include conf%d\n", i+1))
	}
	if _, err := Hosts(home); err == nil {
		t.Fatal("unbounded Include recursion")
	}
}
