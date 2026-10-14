//go:build darwin || linux

package rex

import (
	"net"
	"os"
	"testing"
	"time"

	"retty/internal/agents"
)

func TestAttentionReceiptsAcrossViewersAndNewTurns(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	dir := t.TempDir()
	ss, err := newSession("attention-fixture", CreateOptions{Dir: dir, Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	done := make(chan struct{})
	close(done)
	s := &Server{sessions: map[string]*session{ss.id: ss}, order: []string{ss.id}, hostID: "source", hostDone: done}
	if err := s.loadAttention(dir); err != nil {
		t.Fatal(err)
	}
	client := func() *Client {
		a, b := net.Pipe()
		go s.handle(b)
		c := NewClient(a, nil)
		t.Cleanup(func() { c.Close() })
		return c
	}
	a, b := client(), client()
	h, err := a.Hello()
	if err != nil || !h.Attention {
		t.Fatal("missing capability", h, err)
	}
	state := func(status string) {
		ss.mu.Lock()
		ss.programStatus.feed([]byte(statusOSC("state="+status+":app=fixture")), time.Now())
		ss.mu.Unlock()
	}
	list := func(c *Client) SessionInfo {
		t.Helper()
		rows, err := c.List()
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	state("done")
	before := list(a)
	id := AgentNoticeID(h.Host.ID, before)
	if !AttentionUnread(h.Host.ID, before) {
		t.Fatal("new completion hidden")
	}
	if err := a.MarkAttention(before.ID, id); err != nil {
		t.Fatal(err)
	}
	after := list(b)
	if after.AttentionSeen != id || AttentionUnread(h.Host.ID, after) {
		t.Fatal("second viewer did not receive receipt", after)
	}
	if after.PID != before.PID || after.Cols != 80 || after.Rows != 24 || !after.LastInput.Equal(before.LastInput) {
		t.Fatal("reading changed terminal", after)
	}
	info, err := os.Stat(s.attentionPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("receipt file permissions", info, err)
	}
	loaded := &Server{}
	if err := loaded.loadAttention(dir); err != nil || loaded.attentionSeen[before.ID] != id {
		t.Fatal("receipt lost after service reload", err)
	}
	state("working")
	state("done")
	next := list(b)
	if !AttentionUnread(h.Host.ID, next) {
		t.Fatal("old receipt swallowed a new turn")
	}
	if err := a.MarkAttention(next.ID, id); err == nil {
		t.Fatal("stale receipt accepted")
	}
	state("blocked")
	waiting := list(a)
	if err := a.MarkAttention(waiting.ID, AgentNoticeID(h.Host.ID, waiting)); err != nil {
		t.Fatal(err)
	}
	if !AttentionUnread(h.Host.ID, list(b)) {
		t.Fatal("reading resolved an unanswered question")
	}
	state("working")
	if AttentionUnread(h.Host.ID, list(b)) {
		t.Fatal("resolved wait remains unread")
	}
}

func TestAgentSummaryBelongsToCompletedTurn(t *testing.T) {
	var tracker agentTracker
	now := time.Now()
	apply := func(event, summary string) {
		now = now.Add(time.Millisecond)
		tracker.apply(AgentEvent{Agent: "claude", At: now, Input: agents.HookInput{SessionID: "thread", Event: event, Summary: summary}})
	}
	apply("UserPromptSubmit", "")
	apply("Stop", "本轮已完成。第二句。")
	if tracker.state.Summary != "本轮已完成。" {
		t.Fatal(tracker.state)
	}
	old := tracker.state
	apply("Stop", "")
	if tracker.state.Summary != old.Summary || tracker.state.CompletionRevision != old.CompletionRevision {
		t.Fatal("duplicate completion lost excerpt or added a turn")
	}
	s := SessionInfo{ID: "pane", Title: "旧任务标题", Agent: tracker.state}
	if body := AgentNoticeBody("", s); body != "本轮已完成。" {
		t.Fatal("notification fell back to stale title", body)
	}
	id := AgentNoticeID("source", s)
	s.Agent.Summary = "补充结果。"
	if AgentNoticeID("source", s) != id {
		t.Fatal("updating excerpt generated another reminder")
	}
	apply("UserPromptSubmit", "")
	if tracker.state.Summary != "" {
		t.Fatal("new turn reused previous result")
	}
	apply("Stop", "")
	if tracker.state.Summary != "" {
		t.Fatal("missing final reply reused previous result")
	}
}

func TestAttentionRetainsExplicitOneShotResultOnShell(t *testing.T) {
	t.Setenv("RETTY_DIR", t.TempDir())
	ss, err := newSession("one-shot", CreateOptions{Dir: t.TempDir(), Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	ss.mu.Lock()
	ss.agent.apply(AgentEvent{Agent: "claude", At: time.Now().Add(-4 * time.Second), Input: agents.HookInput{SessionID: "thread", Event: "UserPromptSubmit"}})
	ss.agent.apply(AgentEvent{Agent: "claude", At: time.Now().Add(-3 * time.Second), Input: agents.HookInput{SessionID: "thread", Event: "Stop", Summary: "结果已保存。"}})
	ss.mu.Unlock()
	in := ss.info()
	if !AttentionState(in) || in.Agent.Summary != "结果已保存。" {
		t.Fatal("returning to shell forgot unread result", in)
	}
	ss.mu.Lock()
	ss.agent.apply(AgentEvent{Agent: "claude", At: time.Now(), Input: agents.HookInput{SessionID: "thread", Event: "SessionEnd"}})
	ss.mu.Unlock()
	if AttentionState(ss.info()) {
		t.Fatal("ended conversation retained pending result")
	}
}

func TestCodexSummaryRejectsForeignAndCommentaryItems(t *testing.T) {
	var tracker agentTracker
	o := &codexObserver{pending: map[string]codexThreadRequest{}, emit: func(h agents.HookInput) { tracker.apply(AgentEvent{Agent: "codex", Input: h, At: time.Now()}) }}
	msg := func(raw string) { o.message([]byte(raw), false) }
	o.message([]byte(`{"id":1,"method":"thread/start"}`), true)
	msg(`{"id":1,"result":{"thread":{"id":"own","status":{"type":"idle"}}}}`)
	msg(`{"method":"turn/started","params":{"threadId":"own","turn":{"id":"turn"}}}`)
	for _, raw := range []string{
		`{"method":"item/completed","params":{"threadId":"foreign","turnId":"turn","item":{"type":"agentMessage","text":"foreign","phase":"final_answer"}}}`,
		`{"method":"item/completed","params":{"threadId":"own","turnId":"old","item":{"type":"agentMessage","text":"old","phase":"final_answer"}}}`,
		`{"method":"item/completed","params":{"threadId":"own","turnId":"turn","item":{"type":"agentMessage","text":"commentary","phase":"commentary"}}}`,
	} {
		msg(raw)
	}
	if o.summary != "" {
		t.Fatal("unrelated item retained", o.summary)
	}
	msg(`{"method":"item/completed","params":{"threadId":"own","turnId":"turn","item":{"type":"agentMessage","text":"修复已完成。详细说明。","phase":"final_answer"}}}`)
	if tracker.state.State != agents.Running || tracker.state.Summary != "" {
		t.Fatal("final item prematurely completed the turn")
	}
	msg(`{"method":"turn/completed","params":{"threadId":"own","turn":{"id":"turn","status":"completed"}}}`)
	msg(`{"method":"thread/status/changed","params":{"threadId":"own","status":{"type":"idle"}}}`)
	if tracker.state.Summary != "修复已完成。" {
		t.Fatal("confirmed turn lost summary", tracker.state)
	}
	msg(`{"method":"turn/started","params":{"threadId":"own","turn":{"id":"next"}}}`)
	if tracker.state.Summary != "" || o.summary != "" {
		t.Fatal("next turn inherited summary")
	}
}
