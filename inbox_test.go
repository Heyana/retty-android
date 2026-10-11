package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"retty/internal/agents"
	"retty/internal/rex"
	"retty/internal/terminal"
)

func inboxFixture(sid, state string) rex.SessionInfo {
	return rex.SessionInfo{ID: sid, Title: "任务 " + sid, Program: "codex", Agent: rex.AgentState{ID: "codex", SessionID: "thread-" + sid, State: state, Reason: "question", CompletionRevision: 1, WaitRevision: 1, Updated: time.Now(), Summary: "当前轮连接问题已修复。"}}
}

func TestInboxReceiptsAndPriority(t *testing.T) {
	in := inboxState{}
	s := inboxFixture("done", agents.Completed)
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
	items := []inboxItem{{session: inboxFixture("done", agents.Completed)}, {session: inboxFixture("failed", agents.Failed)}, {session: inboxFixture("wait", agents.Waiting)}}
	sortInbox(items)
	if items[0].session.ID != "wait" || items[1].session.ID != "failed" {
		t.Fatal("waiting and failed results did not take priority")
	}
}

func TestDesktopInboxOpensCorrectPaneAndKeepsQuestions(t *testing.T) {
	prev := prefs
	prefs = settings{FontSize: defaultFontSize}
	t.Cleanup(func() { prefs = prev })
	a, tt := newStaticTestApp(t)
	p := a.tab().Focus
	p.SID = "question"
	p.info = inboxFixture(p.SID, agents.Waiting)
	tt.Frame()
	if err := tt.Click("待处理 · 1"); err != nil {
		t.Fatal(err)
	}
	if !a.inbox.open {
		t.Fatal("Inbox did not open")
	}
	if _, ok := tt.Find("待处理面板"); !ok {
		t.Fatal("missing dialog")
	}
	saveSettingsImage(t, tt, "inbox-desktop-light")
	tt.SetDark(true)
	saveSettingsImage(t, tt, "inbox-desktop-dark")
	if err := tt.Click("打开待处理：任务 question"); err != nil {
		t.Fatal(err)
	}
	if a.inbox.open || a.tab().Focus != p || len(a.inboxItems()) != 1 {
		t.Fatal("opening question changed its state")
	}
	p.info = inboxFixture(p.SID, agents.Completed)
	a.inbox.open = true
	tt.Frame()
	if err := tt.Click("标为已读：任务 question"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.inboxItems()) != 0 {
		t.Fatal("read result remains in Inbox")
	}
	tt.Key(0, ui.KeyEscape)
	if a.inbox.open || !tt.Focused("Terminal") {
		t.Fatal("Escape did not restore terminal focus")
	}
	// Sidebar and collapsed sidebar expose the same Inbox model.
	prefs.DesktopLayout = "sidebar"
	tt.Frame()
	if _, ok := tt.Find("待处理"); !ok {
		t.Fatal("sidebar lost Inbox")
	}
	prefs.SidebarCollapsed = true
	tt.Frame()
	if _, ok := tt.Find("待处理"); !ok {
		t.Fatal("collapsed sidebar lost Inbox")
	}
}

func TestMobileInboxCachesSourcesAndFitsScreens(t *testing.T) {
	registerFonts()
	m := &mobileApp{hello: rex.Hello{Version: 6, Host: rex.HostInfo{ID: "source", Name: "Mac mini"}}}
	m.sessions = []rex.SessionInfo{inboxFixture("wait", agents.Waiting), inboxFixture("done", agents.Completed)}
	m.cacheInbox()
	m.hello.Host.ID = "second"
	m.sessions = []rex.SessionInfo{inboxFixture("failed", agents.Failed)}
	m.cacheInbox()
	if len(m.inboxItems()) != 3 {
		t.Fatal("other computer's pending state was lost")
	}
	for _, size := range [][2]int{{320, 568}, {390, 750}, {750, 310}} {
		m.inbox.open = true
		tt := ui.NewTester(m.view, size[0], size[1])
		panel, ok := tt.Find("待处理面板")
		if !ok || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > float32(size[0]) || panel.Y+panel.H > float32(size[1]) {
			t.Fatal("clipped mobile Inbox", size, panel)
		}
		if size == [2]int{390, 750} {
			saveSettingsImage(t, tt, "inbox-mobile-light")
			tt.SetDark(true)
			saveSettingsImage(t, tt, "inbox-mobile-dark")
		}
		if err := tt.Click("关闭待处理"); err != nil {
			t.Fatal(err)
		}
		if m.inbox.open {
			t.Fatal("dialog did not close")
		}
	}
}

func TestInboxIncludesUnopenedSourceSessionsAndDeduplicatesPanes(t *testing.T) {
	s := inboxFixture("remote-result", agents.Completed)
	h := &desktopHost{key: "source", hello: rex.Hello{Version: 6, Host: rex.HostInfo{ID: "source", Name: "Server"}}, sessions: []rex.SessionInfo{s}}
	a := &App{desktops: desktopConnections{hosts: []*desktopHost{h}}}
	items := a.inboxItems()
	if len(items) != 1 || items[0].source != h || !items[0].cached {
		t.Fatal("unopened remote session missing", items)
	}
	p := &Pane{SID: s.ID, host: h, info: s}
	tab := &Tab{Host: h, Focus: p, Root: &Node{Pane: p}}
	p.Tab = tab
	a.tabs = []*Tab{tab}
	if len(a.inboxItems()) != 1 {
		t.Fatal("same pane and source session duplicated")
	}
	s.AttentionSeen = items[0].id
	p.info = s
	h.sessions = []rex.SessionInfo{s}
	if len(a.inboxItems()) != 0 {
		t.Fatal("source receipt did not clear all duplicate views")
	}
}

func TestMobileInboxReadsOnlyForegroundSelectedResult(t *testing.T) {
	m := &mobileApp{client: &rex.Client{}, hello: rex.Hello{Version: 6, Host: rex.HostInfo{ID: "source"}}, term: &terminal.Terminal{}, selected: rex.SessionInfo{ID: "viewed"}}
	sessions := []rex.SessionInfo{inboxFixture("viewed", agents.Completed), inboxFixture("other", agents.Completed), inboxFixture("wait", agents.Waiting)}
	m.updateSessions(sessions, true)
	items := m.inboxItems()
	if len(items) != 2 {
		t.Fatal("list events were incorrectly read", items)
	}
	for _, item := range items {
		if item.session.ID == "viewed" {
			t.Fatal("visible completed result stayed unread")
		}
	}
	sessions[0].Agent.CompletionRevision++
	m.background = true
	m.updateSessions(sessions, false)
	if len(m.inboxItems()) != 3 {
		t.Fatal("background terminal read a result")
	}
	m.background = false
	m.inbox.open = true
	m.updateSessions(sessions, false)
	if len(m.inboxItems()) != 3 {
		t.Fatal("Inbox overlay read the obscured terminal")
	}
	m.inbox.open = false
	m.updateSessions(sessions, false)
	if len(m.inboxItems()) != 2 {
		t.Fatal("visible result did not become read")
	}
	m.readInbox(sessions[2])
	if len(m.inboxItems()) != 2 {
		t.Fatal("reading a wait resolved it")
	}
	// Forgetting all sources must also remove cached pending items.
	m.term = nil
	m.client = nil
	m.disconnect(true)
	if len(m.inboxItems()) != 0 {
		t.Fatal("forgotten source retained Inbox entries")
	}
}
