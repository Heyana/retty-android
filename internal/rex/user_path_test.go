//go:build darwin || linux

package rex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResumeExecutableSuppliesCurrentPathAndMissingCLIError(t *testing.T) {
	bin := t.TempDir()
	cli := filepath.Join(bin, "fixture-agent")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	previous := interactivePath
	interactivePath = func() string { t.Fatal("unneeded login shell lookup"); return "" }
	t.Cleanup(func() { interactivePath = previous })
	path, env, err := ResumeExecutable("fixture-agent")
	if err != nil || path != cli || !reflect.DeepEqual(env, []string{"PATH=" + bin}) {
		t.Fatal(path, env, err)
	}
	interactivePath = func() string { return "/usr/bin:/bin" }
	if _, _, err := ResumeExecutable("retty-missing-fixture-agent"); err == nil {
		t.Fatal("missing CLI accepted")
	}
}

func TestResumeExecutableUsesNativeInstallWhenLoginShellFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(bin, "claude")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := interactivePath
	interactivePath = func() string { return "" }
	t.Cleanup(func() { interactivePath = previous })
	path, env, err := ResumeExecutable("claude")
	if err != nil || path != cli || !reflect.DeepEqual(env, []string{"PATH=/usr/bin:/bin:" + bin}) {
		t.Fatal(path, env, err)
	}
}

func TestInteractivePathIgnoresStartupAndExitOutput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "login-shell")
	script := "#!/bin/sh\nprintf '\\033]7;file://host/home\\007\\000RETTY_PATH\\000/custom/bin:/usr/bin:/bin\\n\\000exit banner\\n'\n"
	if err := os.WriteFile(file, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", file)
	if got := readInteractivePath(); got != "/custom/bin:/usr/bin:/bin" {
		t.Fatalf("startup output corrupted PATH: %q", got)
	}
}
