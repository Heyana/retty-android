package files

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func gitDiff(ctx context.Context, path string) ([]byte, error) {
	st, statErr := os.Lstat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if statErr == nil && !st.Mode().IsRegular() {
		return nil, errors.New("此文件不支持 Git 文本变更")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dir := existingGitDirectory(filepath.Dir(path))
	name, err := filepath.Rel(dir, path)
	if err != nil {
		return nil, err
	}
	run := func(args ...string) ([]byte, error) { return runGit(ctx, dir, args...) }
	_, err = run("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("当前目录不在 Git 仓库中")
	}
	// Compare the entire working tree file against HEAD, including staged
	// changes. New files and repositories without a first commit use /dev/null.
	base := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3"}
	_, headErr := run("rev-parse", "--verify", "HEAD")
	_, trackedErr := run("ls-files", "--error-unmatch", "--", name)
	if statErr != nil && (headErr != nil || trackedErr != nil) {
		// A staged deletion is no longer in the index; check HEAD directly.
		prefix, _ := run("rev-parse", "--show-prefix")
		_, inHead := run("cat-file", "-e", "HEAD:"+strings.TrimSuffix(string(prefix), "\n")+filepath.ToSlash(name))
		if headErr != nil || inHead != nil {
			return nil, statErr
		}
		trackedErr = nil
	}
	if headErr == nil && trackedErr == nil {
		base = append(base, "HEAD")
	}
	if trackedErr != nil || headErr != nil {
		base = append(base, "--no-index")
	}
	args := append(base, "--", name)
	if trackedErr != nil || headErr != nil {
		args = append(base, "--", os.DevNull, name)
	}
	data, err := run(args...)
	if exit := new(exec.ExitError); errors.As(err, &exit) && exit.ExitCode() == 1 && (trackedErr != nil || headErr != nil) {
		err = nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("无法读取 Git 变更，或变更超过 4MB")
	}
	return data, nil
}

// DiffRow is a display row for a unified patch. Like godiff's viewer, code
// rows retain both line numbers; file headers are not mistaken for code.
type DiffRow struct {
	Text     string
	Kind     byte
	Old, New int
}

func PatchRows(text string) []DiffRow {
	var rows []DiffRow
	old, new := 0, 0
	hunk := false
	for i, line := range strings.SplitN(strings.TrimSuffix(text, "\n"), "\n", MaxPreviewRows+1) {
		if i == MaxPreviewRows {
			rows = append(rows, DiffRow{Kind: '!', Text: "变更较长，仅显示前 20000 行"})
			break
		}
		if strings.HasPrefix(line, "@@ ") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				old = patchStart(fields[1])
				new = patchStart(fields[2])
			}
			hunk = true
			rows = append(rows, DiffRow{Text: line, Kind: '@'})
			continue
		}
		if strings.HasPrefix(line, "diff --git ") {
			hunk = false
		}
		r := DiffRow{Text: line, Kind: '@'}
		if hunk && len(line) > 0 {
			r.Kind = line[0]
			r.Text = line[1:]
			switch r.Kind {
			case '+':
				r.New = new
				new++
			case '-':
				r.Old = old
				old++
			case ' ':
				r.Old, r.New = old, new
				old++
				new++
			default:
				r.Text = line
			}
		}
		rows = append(rows, r)
	}
	return rows
}
func patchStart(s string) int { n, _, _ := strings.Cut(s[1:], ","); i, _ := strconv.Atoi(n); return i }
