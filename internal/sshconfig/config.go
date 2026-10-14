// Package sshconfig discovers concrete Host aliases without evaluating SSH
// options or executing Match commands. OpenSSH resolves options when connecting.
package sshconfig

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Hosts lists aliases in declaration order from ~/.ssh/config and its Include
// files. Wildcards, negations and duplicates are not connection destinations.
// Missing files (including an absent config) are allowed, as in OpenSSH.
func Hosts(home string) ([]string, error) {
	l := loader{home: home, files: make(map[string]bool), aliases: make(map[string]bool), remaining: 8 << 20}
	err := l.read(filepath.Join(home, ".ssh", "config"), 0)
	return l.hosts, err
}

type loader struct {
	home      string
	files     map[string]bool
	aliases   map[string]bool
	hosts     []string
	remaining int64
}

func (l *loader) read(path string, depth int) error {
	path = filepath.Clean(path)
	if l.files[path] {
		return nil
	}
	if depth > 16 || len(l.files) >= 256 {
		return fmt.Errorf("SSH Include limit exceeded")
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	l.files[path] = true
	data, err := io.ReadAll(io.LimitReader(f, l.remaining+1))
	if err != nil {
		return err
	}
	l.remaining -= int64(len(data))
	if l.remaining < 0 {
		return fmt.Errorf("SSH config exceeds 8 MiB")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		i := strings.IndexAny(text, "= \t")
		if i < 0 {
			continue
		}
		key := strings.ToLower(text[:i])
		if key != "host" && key != "include" {
			continue
		}
		value := strings.TrimSpace(text[i:])
		value = strings.TrimSpace(strings.TrimPrefix(value, "="))
		args, err := words(value)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
		for _, arg := range args {
			if key == "host" {
				if concreteHost(arg) && !l.aliases[arg] {
					l.aliases[arg] = true
					l.hosts = append(l.hosts, arg)
				}
				continue
			}
			pattern := arg
			if pattern == "~" {
				pattern = l.home
			} else if tail, ok := strings.CutPrefix(pattern, "~/"); ok {
				pattern = filepath.Join(l.home, tail)
			}
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(l.home, ".ssh", pattern)
			}
			paths, err := filepath.Glob(pattern)
			if err != nil {
				return fmt.Errorf("%s:%d: invalid Include: %w", path, line, err)
			}
			for _, included := range paths {
				if err := l.read(included, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return scanner.Err()
}

func concreteHost(host string) bool {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, "*?!@") {
		return false
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// words accepts quoted arguments, escapes, and comments outside quotes.
func words(value string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			args = append(args, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range value {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped, started = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == '#':
			flush()
			return args, nil
		case unicode.IsSpace(r):
			flush()
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unfinished quoted argument")
	}
	flush()
	return args, nil
}
