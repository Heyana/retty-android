//go:build darwin || linux

package rex

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ResumeExecutable finds an Agent CLI for a resumed pane. The daemon starts
// from a login shell that is not interactive, so PATH entries added in
// ~/.zshrc (where installers such as Claude Code's put ~/.local/bin) are
// missing after a reboot, although every new pane's shell has them. Look the
// CLI up as such a shell would and give the Agent that PATH too, so the tools
// it runs resolve as they did in the original pane. GUI callers can pass the
// absolute path and PATH to an already running daemon with an older environment.
// This can start a login shell, so UI callers must run it off the UI thread.
func ResumeExecutable(name string) (path string, env []string, err error) {
	if path, err = exec.LookPath(name); err == nil {
		return path, []string{"PATH=" + os.Getenv("PATH")}, nil
	}
	userPath := interactivePath()
	if candidate := executableOnPath(name, userPath); candidate != "" {
		return candidate, []string{"PATH=" + userPath}, nil
	}
	// Claude's native installer uses this directory. It remains usable even
	// while login scripts are unavailable or still settling after a reboot.
	if home, e := os.UserHomeDir(); e == nil {
		localBin := filepath.Join(home, ".local", "bin")
		if userPath == "" {
			userPath = os.Getenv("PATH")
		}
		userPath += string(os.PathListSeparator) + localBin
	}
	if candidate := executableOnPath(name, userPath); candidate != "" {
		return candidate, []string{"PATH=" + userPath}, nil
	}
	return "", nil, err
}

func executableOnPath(name, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if st, e := os.Stat(candidate); e == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

var shellPath struct {
	sync.Mutex
	value string
	at    time.Time
}

// interactivePath returns PATH as the user's interactive login shell sets
// it, or "" when that shell fails. After a reboot several panes restore at
// once, so a result is reused briefly rather than starting a shell for each.
var interactivePath = func() string {
	shellPath.Lock()
	defer shellPath.Unlock()
	if shellPath.value != "" && time.Since(shellPath.at) < time.Minute {
		return shellPath.value
	}
	value := readInteractivePath()
	if value != "" {
		shellPath.value, shellPath.at = value, time.Now()
	}
	return value
}

func readInteractivePath() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = defaultShell
	}
	// Startup files may hang waiting for input that never comes; bound them.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, "-l", "-i", "-c", `printf '\000RETTY_PATH\000'; command printenv PATH; printf '\000'`)
	cmd.Dir, _ = os.UserHomeDir()
	// Without a controlling terminal, job control cannot touch another one.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Helpers that startup files leave running (gitstatusd and the like) can
	// hold the output pipe open after the shell exits.
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	// Prompt integration can print OSC escapes without a newline, and exit
	// hooks can print after PATH. Delimit the result instead of taking a line.
	_, rest, ok := bytes.Cut(out, []byte("\x00RETTY_PATH\x00"))
	if !ok {
		return ""
	}
	data, _, ok := bytes.Cut(rest, []byte{0})
	value := strings.TrimSpace(string(data))
	if !ok || !strings.Contains(value, "/") || strings.ContainsAny(value, "\x1b\r\n") {
		return ""
	}
	return value
}
