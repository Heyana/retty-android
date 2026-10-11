package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"retty/internal/agents"
	"retty/internal/rex"
)

// Read receipts are separate from notification delivery. Receiving a push or
// visiting the session list does not mean the result has been read.
type inboxState struct {
	open, loaded bool
	seen         map[string]time.Time
	retry        map[string]time.Time
	err          string
}

type inboxItem struct {
	id, desktop, title, host, body string
	cachedLabel                    string
	session                        rex.SessionInfo
	pane                           *Pane
	source                         *desktopHost
	cached                         bool
}

func inboxNoticeID(h rex.Hello, desktop string, s rex.SessionInfo) string {
	if h.Version < 5 {
		s.Agent.CompletionRevision = 0
	}
	return rex.AgentNoticeID(desktop, s)
}

func (in *inboxState) unread(id string, s rex.SessionInfo) bool {
	return rex.AttentionState(s) && (s.Agent.State == agents.Waiting || s.AttentionSeen != id && in.seen[id].IsZero())
}

func (in *inboxState) remember(id string) {
	if in.seen == nil {
		in.seen = map[string]time.Time{}
	}
	in.seen[id] = time.Now()
	trimReceipts(in.seen, 256)
}

// trimReceipts keeps the newest limit receipts of seen, by when they were
// made, and the first of their ids for those made at once.
func trimReceipts(seen map[string]time.Time, limit int) {
	if len(seen) <= limit {
		return
	}
	keys := slices.Collect(maps.Keys(seen))
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(seen[b].Compare(seen[a]), strings.Compare(a, b))
	})
	for _, k := range keys[limit:] {
		delete(seen, k)
	}
}

// readable tells that the result of a session can be marked read now:
// waiting for input is never read, and a failed sync waits out its retry.
func (in *inboxState) readable(id string, s rex.SessionInfo) bool {
	return s.Agent.State != agents.Waiting && in.unread(id, s) && !time.Now().Before(in.retry[id])
}

// markRead keeps the receipt here and, given a client, on the server of
// sid too; persist saves the receipts. If the server cannot take it, post
// runs on the main thread what forgets the receipt and retries it later.
func (in *inboxState) markRead(client *rex.Client, sid, id string, persist func(), post func(func())) {
	in.remember(id)
	in.err = ""
	delete(in.retry, id)
	persist()
	if client == nil {
		return
	}
	go func() {
		if client.MarkAttention(sid, id) == nil {
			return
		}
		post(func() {
			delete(in.seen, id)
			if in.retry == nil {
				in.retry = map[string]time.Time{}
			}
			in.retry[id] = time.Now().Add(30 * time.Second)
			in.err = "已读状态同步失败，稍后重试"
			persist()
		})
	}()
}

func sortInbox(items []inboxItem) {
	priority := func(s string) int {
		if s == agents.Waiting {
			return 0
		}
		if s == agents.Failed {
			return 1
		}
		return 2
	}
	slices.SortStableFunc(items, func(a, b inboxItem) int {
		return cmp.Or(
			priority(a.session.Agent.State)-priority(b.session.Agent.State),
			b.session.Agent.Updated.Compare(a.session.Agent.Updated),
			strings.Compare(a.id, b.id),
		)
	})
}

func (a *App) inboxItems() []inboxItem {
	var items []inboxItem
	seen := map[string]bool{}
	for t, p := range a.allPanes() {
		if p.closed {
			continue
		}
		h := a.hostHello(p.host)
		id := inboxNoticeID(h, h.Host.ID, p.info)
		if seen[id] || !a.inbox.unread(id, p.info) {
			continue
		}
		seen[id] = true
		title := cmp.Or(t.Name, mobileSessionTitle(p.info))
		host, dir, cached := h.Host.Name, "", false
		if a.inbox.open {
			dir = rex.Dir()
		}
		if p.host != nil {
			host, dir, cached = p.host.name(), "", !p.host.connected()
		}
		if host == "" {
			host = "本机"
		}
		items = append(items, inboxItem{id: id, desktop: h.Host.ID, title: title, host: host, body: rex.AgentNoticeBody(dir, p.info), session: p.info, pane: p, cached: cached})
	}
	// Sources can have sessions that have not been opened here yet.
	for _, h := range a.desktops.hosts {
		for _, s := range h.sessions {
			id := inboxNoticeID(h.hello, h.hello.Host.ID, s)
			if seen[id] || !a.inbox.unread(id, s) {
				continue
			}
			seen[id] = true
			items = append(items, inboxItem{id: id, desktop: h.hello.Host.ID, title: mobileSessionTitle(s), host: h.name(), body: rex.AgentNoticeBody("", s), session: s, source: h, cached: !h.connected()})
		}
	}
	sortInbox(items)
	return items
}

func (a *App) openInbox() {
	a.settingsOpen, a.paletteOpen, a.desktops.open, a.inbox.open = false, false, false, true
	a.focusReq = nil
}

func inboxLabel(count int) string {
	if count > 0 {
		return fmt.Sprintf("待处理 · %d", count)
	}
	return "待处理"
}

func (a *App) inboxButton(c *ui.Context, k *colors, n int) {
	if compactTitleButton(c.Key("inbox-open"), k, "bell", inboxLabel(n), n > 0).Tooltip(inboxLabel(n) + "  ⇧⌘I").Clicked() {
		a.openInbox()
	}
}

func inboxDialog(c *ui.Context, in *inboxState, items []inboxItem, open func(inboxItem), read func(inboxItem)) bool {
	if !in.open {
		return false
	}
	w, h := c.Size()
	dialog := ui.DialogBase(c.Key("inbox-dialog"), &in.open, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4)).Padding(12)
		panel.Label("待处理面板").Width(min(w-24, 560)).Height(min(h-48, float32(min(max(len(items), 1), 5)*92+100))).MinHeight(0).Radius(16).Padding(16).Gap(12).Background(c.Theme().Surface).Column()
		ui.Row(c).FillWidth().Height(44).AlignItems(ui.Center).Children(func() {
			ui.Text(c, inboxLabel(len(items))).FontSize(17).Bold().Grow(1)
			if ui.ButtonBase(c).Label("关闭待处理").Size(44, 44).Radius(22).Children(func() { ui.Icon(c, icon("x")).Size(16, 16) }).Clicked() {
				in.open = false
			}
		})
		if in.err != "" {
			ui.Text(c, in.err).FontSize(12).TextColor(c.Theme().Danger).FillWidth()
		}
		ui.Scroll(c.Key("inbox-items")).Grow(1).MinHeight(0).FillWidth().Gap(8).Children(func() {
			if len(items) == 0 {
				ui.Text(c, "暂无待处理事项").FontSize(14).TextColor(c.Theme().TextMuted).Padding(20).FillWidth()
			}
			for _, item := range items {
				ui.Row(c.Key(item.id)).FillWidth().MinHeight(84).Gap(4).Radius(10).Background(c.Theme().Background).Children(func() {
					row := ui.ButtonBase(c).Label("打开待处理：" + item.title).Grow(1).MinWidth(0).Padding(12).Column().AlignItems(ui.Start).Gap(5)
					row.Children(func() {
						ui.Text(c, item.title).FontSize(14).Bold().FillWidth().SingleLine().Ellipsis("…")
						meta := item.host + " · " + agentStateLabel(item.session.Agent)
						if item.cached {
							label := item.cachedLabel
							if label == "" {
								label = "连接已中断"
							}
							meta += " · " + label
						}
						ui.Text(c, meta).FontSize(11).TextColor(c.Theme().TextMuted).FillWidth().SingleLine().Ellipsis("…")
						ui.Text(c, item.body).FontSize(12).TextColor(c.Theme().TextMuted).FillWidth().SingleLine().Ellipsis("…")
					})
					if row.Clicked() {
						open(item)
					}
					if item.session.Agent.State != agents.Waiting && !item.cached {
						if ui.ButtonBase(c).Label("标为已读："+item.title).Size(44, 44).Shrink(0).Children(func() { ui.Icon(c, icon("check")).Size(16, 16).TextColor(c.Theme().TextMuted) }).Tooltip("标为已读").Clicked() {
							read(item)
						}
					}
				})
			}
		})
	})
	return dialog.Dismissed() || !in.open
}

func (a *App) inboxView(c *ui.Context, items []inboxItem) {
	if inboxDialog(c, &a.inbox, items, func(item inboxItem) {
		if item.pane != nil {
			if a.focusAgentPane(item.pane.noticeKey()) {
				a.readInbox(item.pane)
			}
		} else if item.source != nil {
			a.inbox.open = false
			if item.source.connected() {
				a.openDesktopSession(item.source, item.session, true)
				a.readInbox(&Pane{SID: item.session.ID, info: item.session, host: item.source})
			} else {
				item.source.pendingSession = item.session.ID
				a.connectDesktop(item.source)
			}
		}
	}, func(item inboxItem) {
		p := item.pane
		if p == nil {
			p = &Pane{SID: item.session.ID, info: item.session, host: item.source}
		}
		a.readInbox(p)
	}) {
		if t := a.tab(); t != nil {
			a.focusReq = t.Focus
		}
	}
}

func (a *App) readInbox(p *Pane) {
	if p == nil || p.closed || p.host != nil && !p.host.connected() {
		return
	}
	h := a.hostHello(p.host)
	id := inboxNoticeID(h, h.Host.ID, p.info)
	if !a.inbox.readable(id, p.info) {
		return
	}
	client := a.paneClient(p)
	if !h.Attention {
		client = nil
	}
	a.inbox.markRead(client, p.SID, id, a.persistInbox, a.desktopDispatcher())
}

func (a *App) loadInbox() {
	if a.inbox.loaded {
		return
	}
	a.inbox.loaded = true
	data, err := os.ReadFile(filepath.Join(rex.Dir(), "inbox-seen.json"))
	if err == nil && len(data) <= 128<<10 {
		json.Unmarshal(data, &a.inbox.seen)
	}
	trimReceipts(a.inbox.seen, 256)
}

func (a *App) persistInbox() {
	if a.win == nil {
		return
	} // Headless fixture views never write application data.
	data, _ := json.Marshal(a.inbox.seen)
	dir := rex.Dir()
	if a.desktopStorage == nil {
		a.desktopStorage = connectionStorage()
	}
	a.desktopStorage <- func() {
		f, err := os.CreateTemp(dir, ".inbox-*")
		if err != nil {
			return
		}
		defer os.Remove(f.Name())
		_, err = f.Write(data)
		closed := f.Close()
		if err == nil && closed == nil {
			os.Rename(f.Name(), filepath.Join(dir, "inbox-seen.json"))
		}
	}
}
