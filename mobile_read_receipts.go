package main

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo"
	"retty/internal/rex"
)

func (m *mobileApp) resultTerminalVisible() bool {
	return m.term != nil && !m.background && !m.home && (m.win == nil || m.screenActive) &&
		!m.connectionDetailsOpen && !m.connectionDiagnosticsOpen &&
		!m.sessionSettingsOpen && !m.editingOpen && !m.endingOpen && !m.deviceNameOpen
}

func (m *mobileApp) readResult(s rex.SessionInfo) {
	if m.background || !m.connectionUsable() {
		return
	}
	id := m.taskNoticeID(s)
	if !m.readReceipts.readable(id, s) {
		return
	}
	client := m.client
	if !m.hello.Attention {
		client = nil
	}
	m.readReceipts.markRead(client, s.ID, id, m.persistReadReceipts, func(retry func()) {
		mygo.RunOnMain(func() { retry(); m.invalidate() })
	})
}

func (m *mobileApp) loadReadReceipts() {
	if m.store == nil || m.storage == nil {
		return
	}
	m.storage <- func() {
		// Keep the existing Keychain key so viewed results stay read.
		data, err := m.store.Get("inbox-seen-v1")
		seen := map[string]time.Time{}
		if err == nil && len(data) <= 128<<10 {
			json.Unmarshal(data, &seen)
		}
		mygo.RunOnMain(func() {
			if m.readReceipts.seen == nil {
				m.readReceipts.seen = map[string]time.Time{}
			}
			for k, v := range seen {
				if m.readReceipts.seen[k].Before(v) {
					m.readReceipts.seen[k] = v
				}
			}
			trimReceipts(m.readReceipts.seen, 256)
			m.readReceipts.loaded = true
			m.persistReadReceipts()
			m.invalidate()
		})
	}
}

func (m *mobileApp) persistReadReceipts() {
	if m.readReceipts.loaded {
		data, _ := json.Marshal(m.readReceipts.seen)
		m.save("inbox-seen-v1", data, nil)
	}
}
