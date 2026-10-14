package rex

import (
	"testing"
	"time"
)

func TestAgentNoticeIdentityIsStableAndSeparatesTasksAndDevices(t *testing.T) {
	now := time.Now()
	s := SessionInfo{ID: "pane", LastInput: now, Agent: AgentState{ID: "codex", SessionID: "thread", State: "completed", CompletionRevision: 2, Updated: now}}
	id := AgentNoticeID("desktop", s)
	s.Agent.Updated = now.Add(time.Second)
	if AgentNoticeID("desktop", s) != id {
		t.Fatal("duplicate stop changed notice identity")
	}
	if AgentNoticeID("other-desktop", s) == id {
		t.Fatal("desktop collision")
	}
	s.Agent.CompletionRevision++
	if AgentNoticeID("desktop", s) == id {
		t.Fatal("distinct task reused identity")
	}
	s.Agent.CompletionRevision = 0
	id = AgentNoticeID("desktop", s)
	s.LastInput = now.Add(time.Second)
	if AgentNoticeID("desktop", s) != id {
		t.Fatal("draft editing changed legacy completion identity")
	}
	s.Agent.Updated = now.Add(2 * time.Second)
	if AgentNoticeID("desktop", s) == id {
		t.Fatal("new legacy completion reused identity")
	}
}

func TestLegacyCompletionNeedsNewCompletionAfterInput(t *testing.T) {
	now := time.Now()
	previous := SessionInfo{ID: "pane", LastInput: now.Add(-time.Second), Agent: AgentState{ID: "codex", SessionID: "thread", State: "completed", Updated: now}}
	next := previous
	next.LastInput = now.Add(9 * time.Minute)
	if AgentNoticeTransition(previous, next) {
		t.Fatal("editing draft replayed an old completion")
	}
	next.Agent.Updated = next.LastInput.Add(time.Second)
	if !AgentNoticeTransition(previous, next) {
		t.Fatal("fast new turn lost between polls")
	}
	previous.Agent = LegacyCompletionState(SessionInfo{}, previous)
	duplicate := previous
	duplicate.Agent.CompletionRevision = 0
	duplicate.Agent.Updated = now.Add(time.Second)
	duplicate.Agent = LegacyCompletionState(previous, duplicate)
	if duplicate.Agent != previous.Agent {
		t.Fatal("duplicate hook changed the anchored completion", duplicate.Agent)
	}
}

func TestAgentTitleCompletionDoesNotNeedAttention(t *testing.T) {
	previous := SessionInfo{Agent: AgentState{ID: "codex", SessionID: "thread", State: "running"}}
	title := previous
	title.Agent.State, title.Agent.CompletionRevision = "completed", 1
	title.Agent.Summary = `{"title":"提交 relaykit-bridge PR"}`
	if AgentNoticeState(title) || AgentNoticeTransition(previous, title) || AttentionState(title) {
		t.Fatal("internal title generation requested attention")
	}
	for _, tc := range []struct{ name, agent, state, source, summary string }{
		{"real result", "codex", "completed", "", "PR 已提交。"},
		{"failure", "codex", "failed", "", title.Agent.Summary},
		{"other agent", "claude", "completed", "", title.Agent.Summary},
		{"program status", "codex", "completed", ProgramStatusSource, title.Agent.Summary},
		{"other structured output", "codex", "completed", "", `{"title":"PR","url":"https://example.com/pr"}`},
		{"invalid JSON", "codex", "completed", "", `{"title":"PR"`},
		{"non-string title", "codex", "completed", "", `{"title":123}`},
		{"empty title", "codex", "completed", "", `{"title":" "}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := title
			next.Agent.ID, next.Agent.State, next.Agent.Source, next.Agent.Summary = tc.agent, tc.state, tc.source, tc.summary
			next.Agent.CompletionRevision++
			if !AgentNoticeState(next) || !AgentNoticeTransition(title, next) {
				t.Fatal("normal result suppressed after a naming task")
			}
		})
	}
}
