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

// resumeExecutable finds an Agent CLI for a resumed pane. The daemon starts
// from a login shell that is not interactive, so PATH entries added in
// ~/.zshrc (where installers such as Claude Code's put ~/.local/bin) are
// missing after a reboot, although every new pane's shell has them. Look the
// CLI up as such a shell would and give the Agent that PATH too, so the tools
// it runs resolve as they did in the original pane.
func resumeExecutable(name string) (path string, env []string, err error) {
	if path, err = exec.LookPath(name); err == nil {
		return path, nil, nil
	}
	userPath := interactivePath()
	for _, dir := range filepath.SplitList(userPath) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if st, e := os.Stat(candidate); e == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate, []string{"PATH=" + userPath}, nil
		}
	}
	return "", nil, err
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
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = defaultShell
	}
	// Startup files may hang waiting for input that never comes; bound them.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, "-l", "-i", "-c", "printenv PATH")
	cmd.Dir, _ = os.UserHomeDir()
	// Without a controlling terminal, job control cannot touch another one.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Helpers that startup files leave running (gitstatusd and the like) can
	// hold the output pipe open after the shell exits.
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	// Startup files may print banners first; PATH is the last line.
	lines := strings.Split(strings.TrimSpace(string(bytes.ReplaceAll(out, []byte("\r"), nil))), "\n")
	value := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(value, "/") {
		return ""
	}
	shellPath.value, shellPath.at = value, time.Now()
	return value
}
