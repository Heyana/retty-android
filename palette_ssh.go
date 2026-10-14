package main

import (
	"os"
	"strings"

	"retty/internal/sshconfig"
)

func sshPaletteQuery(query string) (string, bool) {
	query = strings.TrimSpace(query)
	keyword, rest := query, ""
	if i := strings.IndexAny(query, " \t\r\n"); i >= 0 {
		keyword, rest = query[:i], strings.TrimSpace(query[i:])
	}
	return strings.ToLower(rest), strings.EqualFold(keyword, "ssh")
}

// Read once per palette opening, off the UI thread. A reopened palette must
// not receive the previous opening's late result.
func (a *App) loadPaletteSSH() {
	a.paletteSSHEpoch++
	epoch := a.paletteSSHEpoch
	a.paletteSSHHosts, a.paletteSSHError = nil, ""
	a.paletteSSHLoading = true
	win := a.win
	home, homeErr := os.UserHomeDir()
	go func() {
		err := homeErr
		var hosts []string
		if err == nil {
			hosts, err = sshconfig.Hosts(home)
		}
		a.post(func() {
			if epoch != a.paletteSSHEpoch {
				return
			}
			a.paletteSSHLoading = false
			a.paletteSSHHosts = hosts
			if err != nil {
				a.paletteSSHHosts = nil
				a.paletteSSHError = "无法读取 ~/.ssh/config：" + err.Error()
			}
		})
		if win != nil {
			win.Invalidate()
		}
	}()
}

func (a *App) sshPaletteItems(query string) []paletteItem {
	var items []paletteItem
	for _, host := range a.paletteSSHHosts {
		if !fuzzy(strings.ToLower(host), query) {
			continue
		}
		items = append(items, paletteItem{
			id: "ssh:" + host, title: host, detail: "SSH", glyph: "square-terminal",
			run: func() { a.openSSHHost(host) },
		})
	}
	return items
}

func (a *App) paletteEmptyMessage() string {
	query, ssh := sshPaletteQuery(a.paletteQuery)
	if !ssh {
		return "No matches"
	}
	if a.paletteSSHLoading {
		return "正在读取 SSH 主机…"
	}
	if a.paletteSSHError != "" {
		return a.paletteSSHError
	}
	if query == "" && len(a.paletteSSHHosts) == 0 {
		return "~/.ssh/config 中没有具体的 Host 主机"
	}
	return "没有匹配的 SSH 主机"
}

func (a *App) openSSHHost(host string) {
	home, err := os.UserHomeDir()
	if err != nil {
		a.err = err.Error()
		return
	}
	// Always run locally, even when the current pane belongs to another
	// desktop. Pass the alias as one argument; OpenSSH owns all option lookup.
	t := a.newLocalCommandTab(home, []string{"ssh", "--", host})
	t.Name = "SSH " + host
}
