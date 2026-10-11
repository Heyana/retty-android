package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseReadLimitsAndVersion(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "z-folder"), 0700)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0600)
	name := filepath.Join(dir, "中文 ' file.txt")
	os.WriteFile(name, []byte("0123456789"), 0600)
	r, err := Do(context.Background(), Request{Kind: "list", Path: dir})
	if err != nil || len(r.Entries) != 2 || !r.Entries[0].Directory {
		t.Fatal(r, err)
	}
	r, err = Do(context.Background(), Request{Kind: "list", Path: dir, Hidden: true})
	if err != nil || len(r.Entries) != 3 {
		t.Fatal(r, err)
	}
	r, err = Do(context.Background(), Request{Kind: "read", Path: name, Offset: 2, Length: 3})
	if err != nil || string(r.Data) != "234" || !r.Truncated {
		t.Fatal(r, err)
	}
	version := r.Entry.Version
	for _, req := range []Request{{Kind: "read", Path: name, Length: MaxRead + 1}, {Kind: "read", Path: name, Length: 1, Offset: -1}, {Kind: "read", Path: dir, Length: 1}, {Kind: "list", Path: name}, {Kind: "read", Path: "relative.txt", Length: 1}, {Kind: "write", Path: name}} {
		if _, err := Do(context.Background(), req); err == nil {
			t.Fatal("accepted invalid request", req)
		}
	}
	os.WriteFile(name, []byte("changed-and-longer"), 0600)
	if _, err := Do(context.Background(), Request{Kind: "read", Path: name, Length: 10, Version: version}); err == nil {
		t.Fatal("mixed versions accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Do(ctx, Request{Kind: "list", Path: dir}); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestGitPreviewIsReadOnlyAndLiteral(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(args, string(out), err)
		}
		return string(out)
	}
	git("init", "-q")
	name := filepath.Join(dir, "[literal].txt")
	os.WriteFile(name, []byte("old\nshared\n"), 0600)
	git("add", ".")
	git("commit", "-qm", "fixture")
	os.WriteFile(name, []byte("staged\nshared\n"), 0600)
	git("add", ".")
	os.WriteFile(name, []byte("current\nshared\n"), 0600)
	before := git("status", "--porcelain=v1") + git("diff", "--cached")
	r, err := Do(context.Background(), Request{Kind: "diff", Path: name})
	if err != nil || !strings.Contains(string(r.Data), "-old") || !strings.Contains(string(r.Data), "+current") {
		t.Fatal(string(r.Data), err)
	}
	if after := git("status", "--porcelain=v1") + git("diff", "--cached"); after != before {
		t.Fatal("preview modified repository")
	}
	newFile := filepath.Join(dir, "new file.txt")
	os.WriteFile(newFile, []byte("brand new\n"), 0600)
	r, err = Do(context.Background(), Request{Kind: "diff", Path: newFile})
	if err != nil || !strings.Contains(string(r.Data), "+brand new") {
		t.Fatal(string(r.Data), err)
	}
	git("config", "diff.external", "touch "+filepath.Join(dir, "external-must-not-run"))
	if _, err := Do(context.Background(), Request{Kind: "diff", Path: name}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "external-must-not-run")); !os.IsNotExist(err) {
		t.Fatal("external diff executed")
	}
	rows := PatchRows("--- a/file\n+++ b/file\n@@ -3,2 +4,2 @@\n-old\n+new\n shared\n\\ No newline at end of file\n")
	if rows[0].Kind != '@' || rows[1].Kind != '@' || rows[3].Old != 3 || rows[3].New != 0 || rows[4].New != 4 || rows[5].Old != 4 || rows[5].New != 5 {
		t.Fatal(rows)
	}
}

func TestPatchRowCountIsBounded(t *testing.T) {
	rows := PatchRows("@@ -0,0 +1,21000 @@\n" + strings.Repeat("+line\n", MaxPreviewRows+10))
	if len(rows) != MaxPreviewRows+1 || rows[len(rows)-1].Kind != '!' {
		t.Fatal("patch row bound lost", len(rows))
	}
}

func TestGitChangesIncludesNestedDeletionRenameAndLiteralNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(args, string(out), err)
		}
		return string(out)
	}
	git("init", "-q")
	os.MkdirAll(filepath.Join(dir, "gone", "deep"), 0700)
	os.MkdirAll(filepath.Join(dir, "src"), 0700)
	os.WriteFile(filepath.Join(dir, "gone/deep/deleted.go"), []byte("deleted line\n"), 0600)
	os.WriteFile(filepath.Join(dir, "src/staged-delete.go"), []byte("staged deleted line\n"), 0600)
	os.WriteFile(filepath.Join(dir, "src/old name.go"), []byte("rename line\n"), 0600)
	os.WriteFile(filepath.Join(dir, "clean.go"), []byte("clean\n"), 0600)
	os.WriteFile(filepath.Join(dir, "modified.go"), []byte("old\n"), 0600)
	git("add", ".")
	git("commit", "-qm", "fixture")
	os.RemoveAll(filepath.Join(dir, "gone"))
	git("rm", "-q", "src/staged-delete.go")
	git("mv", "src/old name.go", "src/new name.go")
	os.WriteFile(filepath.Join(dir, "modified.go"), []byte("staged\n"), 0600)
	git("add", "modified.go")
	os.WriteFile(filepath.Join(dir, "modified.go"), []byte("current\n"), 0600)
	os.WriteFile(filepath.Join(dir, "src/new\n[literal].go"), []byte("new\n"), 0600)
	before := git("status", "--porcelain=v1", "-z") + git("diff", "--cached")
	r, err := Do(context.Background(), Request{Kind: "changes", Path: filepath.Join(dir, "src")})
	if err != nil || len(r.Entries) != 5 {
		t.Fatal(r, err)
	}
	statuses := map[string]string{}
	for _, e := range r.Entries {
		statuses[filepath.ToSlash(e.Name)] = e.Status
	}
	for name, status := range map[string]string{"gone/deep/deleted.go": "D", "src/staged-delete.go": "D", "src/new name.go": "R", "modified.go": "MM", "src/new\n[literal].go": "??"} {
		if statuses[name] != status {
			t.Fatal(name, statuses)
		}
	}
	for _, name := range []string{"gone/deep/deleted.go", "src/staged-delete.go"} {
		patch, err := Do(context.Background(), Request{Kind: "diff", Path: filepath.Join(r.Path, name)})
		if err != nil || !strings.Contains(string(patch.Data), "deleted line") {
			t.Fatal(name, string(patch.Data), err)
		}
	}
	if after := git("status", "--porcelain=v1", "-z") + git("diff", "--cached"); before != after {
		t.Fatal("browse changed repository")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Do(ctx, Request{Kind: "changes", Path: dir}); err == nil {
		t.Fatal("cancel ignored")
	}
}
