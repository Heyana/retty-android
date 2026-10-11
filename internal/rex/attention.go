package rex

import "retty/internal/agents"

// AttentionState uses the same lifecycle evidence and identity as reminders.
// Viewing a blocked pane does not resolve the question. Completed one-shot
// programs can return to their shell before a viewer receives the event.
func AttentionState(s SessionInfo) bool {
	if s.Exited || !AgentNoticeState(s) {
		return false
	}
	if s.Agent.State == agents.Waiting && s.Agent.Source != ProgramStatusSource {
		a, ok := agents.Detect(s.Program, s.Args)
		return ok && !s.Idle && a.ID == s.Agent.ID
	}
	return true
}

func AttentionUnread(desktop string, s SessionInfo) bool {
	return AttentionState(s) && (s.Agent.State == agents.Waiting || s.AttentionSeen != AgentNoticeID(desktop, s))
}
