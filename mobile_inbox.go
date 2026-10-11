package main

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"retty/internal/rex"
)

// Keep snapshots of previously visited computers without opening extra
// connections. Cached entries must reconnect before they can be read.
func (m *mobileApp) cacheInbox() {
	m.cacheInboxSource(m.hello, m.desktopKey(), m.connectedDevice().displayName(), m.sessions)
}

func (m *mobileApp) cacheInboxSource(hello rex.Hello, desktop, name string, sessions []rex.SessionInfo) {
	if m.inboxCache == nil {
		m.inboxCache = map[string][]inboxItem{}
	}
	var items []inboxItem
	for _, s := range sessions {
		if !rex.AttentionState(s) || m.closedSessions[sessionKey(desktop, s.ID)] {
			continue
		}
		items = append(items, inboxItem{id: inboxNoticeID(hello, desktop, s), desktop: desktop, title: m.titleOn(desktop, s), host: name, body: rex.AgentNoticeBody("", s), session: s})
	}
	m.inboxCache[desktop] = items
}

func (m *mobileApp) inboxItems() []inboxItem {
	var items []inboxItem
	for desktop, entries := range m.inboxCache {
		for _, item := range entries {
			if m.inbox.unread(item.id, item.session) {
				item.cached = desktop != m.desktopKey() || !m.connectionUsable()
				if desktop != m.desktopKey() {
					item.cachedLabel = "点击连接查看"
				}
				items = append(items, item)
			}
		}
	}
	sortInbox(items)
	return items
}

func (m *mobileApp) inboxButton(c *ui.Context) {
	n := len(m.inboxItems())
	if ui.ButtonBase(c.Key("mobile-inbox-open")).Label(inboxLabel(n)).Size(44, 44).Shrink(0).Children(func() {
		ui.Icon(c, icon("bell")).Size(21, 21).TextColor(c.Theme().Accent)
		if n > 0 {
			ui.Text(c, "•").FontSize(14).TextColor(c.Theme().Accent)
		}
	}).Clicked() {
		m.inbox.open = true
		m.sessionSettingsOpen, m.editingOpen, m.endingOpen, m.connectionDetailsOpen = false, false, false, false
		m.connectionDiagnosticsOpen, m.deviceNameOpen = false, false
		c.Blur()
	}
}

func (m *mobileApp) inboxView(c *ui.Context) {
	if inboxDialog(c, &m.inbox, m.inboxItems(), func(item inboxItem) {
		m.inbox.open = false
		m.openNotifiedSession(item.desktop, item.session.ID)
		if !item.cached {
			m.readInbox(item.session)
		}
	}, func(item inboxItem) { m.readInbox(item.session) }) {
		c.Blur()
	}
}

func (m *mobileApp) inboxTerminalVisible() bool {
	return m.term != nil && !m.background && !m.home && (m.win == nil || m.screenActive) &&
		!m.inbox.open && !m.connectionDetailsOpen && !m.connectionDiagnosticsOpen &&
		!m.sessionSettingsOpen && !m.editingOpen && !m.endingOpen && !m.deviceNameOpen
}

func (m *mobileApp) readInbox(s rex.SessionInfo) {
	if m.background || !m.connectionUsable() {
		return
	}
	id := m.taskNoticeID(s)
	if !m.inbox.readable(id, s) {
		return
	}
	client := m.client
	if !m.hello.Attention {
		client = nil
	}
	m.inbox.markRead(client, s.ID, id, m.persistInbox, func(retry func()) {
		mygo.RunOnMain(func() { retry(); m.invalidate() })
	})
}

func (m *mobileApp) loadInbox() {
	if m.store == nil || m.storage == nil {
		return
	}
	m.storage <- func() {
		data, err := m.store.Get("inbox-seen-v1")
		seen := map[string]time.Time{}
		if err == nil && len(data) <= 128<<10 {
			json.Unmarshal(data, &seen)
		}
		mygo.RunOnMain(func() {
			if m.inbox.seen == nil {
				m.inbox.seen = map[string]time.Time{}
			}
			for k, v := range seen {
				if m.inbox.seen[k].Before(v) {
					m.inbox.seen[k] = v
				}
			}
			trimReceipts(m.inbox.seen, 256)
			m.inbox.loaded = true
			m.persistInbox()
			m.invalidate()
		})
	}
}

func (m *mobileApp) persistInbox() {
	if m.inbox.loaded {
		data, _ := json.Marshal(m.inbox.seen)
		m.save("inbox-seen-v1", data, nil)
	}
}
