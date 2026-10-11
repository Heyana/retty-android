package main

import (
	"fmt"
	"testing"
	"time"

	"retty/internal/agents"
	"retty/internal/rex"
	"retty/internal/terminal"
)

func resultFixture(sid, state string) rex.SessionInfo {
	return rex.SessionInfo{ID: sid, Title: "任务 " + sid, Program: "codex", Agent: rex.AgentState{ID: "codex", SessionID: "thread-" + sid, State: state, Reason: "question", CompletionRevision: 1, WaitRevision: 1, Updated: time.Now(), Summary: "当前轮连接问题已修复。"}}
}

func TestReadReceiptsRespectEventIdentity(t *testing.T) {
	in := readReceiptState{}
	s := resultFixture("done", agents.Completed)
	id := rex.AgentNoticeID("desktop", s)
	if !in.unread(id, s) {
		t.Fatal("new completion is read")
	}
	in.remember(id)
	if in.unread(id, s) {
		t.Fatal("viewed result is unread")
	}
	s.Agent.CompletionRevision++
	if !in.unread(rex.AgentNoticeID("desktop", s), s) {
		t.Fatal("new turn was swallowed")
	}
	s.Agent.State = agents.Waiting
	id = rex.AgentNoticeID("desktop", s)
	in.remember(id)
	if !in.unread(id, s) {
		t.Fatal("viewing resolved a question")
	}
	s.Idle = true
	if in.unread(id, s) {
		t.Fatal("stale waiting state appeared on shell")
	}
	for i := 0; i < 300; i++ {
		in.remember(fmt.Sprint(i))
	}
	if len(in.seen) != 256 {
		t.Fatal("unbounded receipt storage", len(in.seen))
	}
}

func TestMobileReadsOnlyForegroundSelectedResult(t *testing.T) {
	m := &mobileApp{client: &rex.Client{}, hello: rex.Hello{Version: 6, Host: rex.HostInfo{ID: "source"}}, term: &terminal.Terminal{}, selected: rex.SessionInfo{ID: "viewed"}}
	sessions := []rex.SessionInfo{resultFixture("viewed", agents.Completed), resultFixture("other", agents.Completed), resultFixture("wait", agents.Waiting)}
	m.updateSessions(sessions, true)
	unread := func() int {
		n := 0
		for _, session := range sessions {
			if m.readReceipts.unread(m.taskNoticeID(session), session) {
				n++
			}
		}
		return n
	}
	if unread() != 2 || m.readReceipts.unread(m.taskNoticeID(sessions[0]), sessions[0]) {
		t.Fatal("only the selected foreground result should be read")
	}
	sessions[0].Agent.CompletionRevision++
	m.background = true
	m.updateSessions(sessions, false)
	if unread() != 3 {
		t.Fatal("background terminal read a result")
	}
	m.background = false
	m.sessionSettingsOpen = true
	m.updateSessions(sessions, false)
	if unread() != 3 {
		t.Fatal("settings overlay read the obscured terminal")
	}
	m.sessionSettingsOpen = false
	m.updateSessions(sessions, false)
	if unread() != 2 {
		t.Fatal("visible result did not become read")
	}
	m.readResult(sessions[2])
	if unread() != 2 {
		t.Fatal("reading a wait resolved it")
	}
}
