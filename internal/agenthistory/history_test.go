package agenthistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, values ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, v := range values {
		if err := json.NewEncoder(f).Encode(v); err != nil {
			t.Fatal(err)
		}
	}
}

func codexMeta(id, dir string) map[string]any {
	return map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "cwd": dir, "source": "cli"}}
}

func TestReadHistoriesRefreshAndDeduplicate(t *testing.T) {
	home := t.TempDir()
	codex, claude := filepath.Join(home, "codex"), filepath.Join(home, "claude")
	project := filepath.Join(home, "project with spaces")
	first := filepath.Join(codex, "sessions/2026/10/16/rollout-one.jsonl")
	write(t, first, codexMeta("thread-one", project), map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "First task"}})
	write(t, filepath.Join(codex, "archived_sessions/rollout-copy.jsonl"), codexMeta("thread-one", project))
	write(t, filepath.Join(codex, "session_index.jsonl"), map[string]any{"id": "thread-one", "thread_name": "Old name"}, map[string]any{"id": "missing", "thread_name": "Deleted conversation"}, map[string]any{"id": "thread-one", "thread_name": "Renamed task"})
	worker := codexMeta("worker-one", project)
	worker["payload"].(map[string]any)["source"] = map[string]any{"subagent": map[string]any{"thread_spawn": true}}
	write(t, filepath.Join(codex, "sessions/rollout-worker.jsonl"), worker)
	worker = codexMeta("worker-two", project)
	worker["payload"].(map[string]any)["thread_source"] = "subagent"
	write(t, filepath.Join(codex, "sessions/rollout-worker2.jsonl"), worker)
	cf := filepath.Join(claude, "projects/encoded/claude-one.jsonl")
	write(t, cf, map[string]any{"type": "user", "sessionId": "claude-one", "cwd": project, "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Claude task"}}}}, map[string]any{"type": "ai-title", "aiTitle": "AI title"}, map[string]any{"type": "custom-title", "customTitle": "My title"})
	write(t, filepath.Join(claude, "projects/encoded/claude-one/subagents/worker.jsonl"), map[string]any{"type": "user", "sessionId": "worker", "cwd": project})
	write(t, filepath.Join(claude, "projects/encoded/sidechain.jsonl"), map[string]any{"type": "user", "sessionId": "sidechain", "cwd": project, "isSidechain": true})
	var r Reader
	read := func(want int) map[string]Session {
		t.Helper()
		list, err := r.Read(codex, claude)
		if err != nil || len(list) != want {
			t.Fatalf("list=%+v error=%v", list, err)
		}
		byID := make(map[string]Session)
		for _, s := range list {
			byID[s.ID] = s
			if s.Dir != project {
				t.Fatalf("wrong project: %+v", s)
			}
		}
		return byID
	}
	got := read(2)
	if got["thread-one"].Title != "Renamed task" || got["claude-one"].Title != "My title" {
		t.Fatal(got)
	}
	// Index renames must update even when the cached transcript is unchanged.
	write(t, filepath.Join(codex, "session_index.jsonl"), map[string]any{"id": "thread-one", "thread_name": "Latest name"})
	if read(2)["thread-one"].Title != "Latest name" {
		t.Fatal("cached transcript hid renamed title")
	}
	os.Remove(cf)
	if read(1)["thread-one"].ID == "" {
		t.Fatal("removed transcript was not evicted")
	}
	write(t, filepath.Join(codex, "sessions/rollout-two.jsonl"), codexMeta("thread-two", project))
	read(2)
}

func TestResumeUsesExactIDDirectoryAndConfig(t *testing.T) {
	dir := t.TempDir()
	for _, agent := range []string{"codex", "claude"} {
		s := Session{Agent: agent, ID: "exact-thread", Dir: dir, ConfigDir: filepath.Join(dir, "config")}
		cmd, env, err := s.Resume()
		want, wantEnv := []string{"claude", "--resume", s.ID}, "CLAUDE_CONFIG_DIR="+s.ConfigDir
		if agent == "codex" {
			want, wantEnv = []string{"codex", "resume", s.ID, "-C", dir}, "CODEX_HOME="+s.ConfigDir
		}
		if err != nil || !reflect.DeepEqual(cmd, want) || !reflect.DeepEqual(env, []string{wantEnv}) {
			t.Fatal(cmd, env, err)
		}
		for _, id := range []string{"--last", "bad;command", "bad\ninput", ""} {
			s.ID = id
			if _, _, err := s.Resume(); err == nil {
				t.Fatal("accepted invalid ID", id)
			}
		}
	}
	s := Session{Agent: "codex", ID: "exact", Dir: filepath.Join(dir, "missing"), ConfigDir: dir}
	if _, _, err := s.Resume(); err == nil {
		t.Fatal("missing project resumed in another directory")
	}
}

func TestClaudeLargeTranscriptTailTitleAndMalformedRecords(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chat.jsonl")
	write(t, file, map[string]any{"type": "user", "sessionId": "thread", "cwd": dir, "message": map[string]any{"content": "Initial title"}})
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(strings.Repeat("x", 3<<20) + "\nnot json\n")
	f.WriteString(`{"type":"custom-title","customTitle":"New title"}` + "\n")
	f.Close()
	s := readSession(file, "claude", dir, time.Now())
	if s.ID != "thread" || s.Title != "New title" || s.Dir != dir {
		t.Fatal(s)
	}
	var r Reader
	if list, err := r.Read(filepath.Join(dir, "absent-codex"), filepath.Join(dir, "absent-claude")); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}
