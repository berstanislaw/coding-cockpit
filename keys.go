package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) dailyKey(k tea.KeyMsg) (model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		if !m.dailyBusy {
			m.dailyBusy = true
			return m, dailyCmd(true, m.settings.language())
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.dailyVP, cmd = m.dailyVP.Update(k)
	return m, cmd
}

func (m model) switchTab(t tab) model {
	if t == m.tab {
		return m
	}
	m.tab, m.sel, m.pending, m.focus = t, 0, "", focusStrip
	pane := ""
	switch t {
	case tabKanban:
		m.focus = focusBoard
		if m.ticket != nil {
			pane = m.bestPane(m.ticket.Identifier)
		}
	case tabSessions:
		pane = m.bestPane(freeKey)
	case tabReviews:
		m.focus = focusBoard
		if m.openPR != nil && m.openPR.Mine {
			pane = m.bestPane(prKey(*m.openPR))
		}
	}
	var err error
	if pane != "" {
		err = showRight(pane)
	} else {
		err = hideRight()
	}
	if err != nil {
		m.err = err
	}
	return m
}

func (m model) handleKey(k tea.KeyMsg) (model, tea.Cmd) {
	key := k.String()
	if m.showKeys {
		m.showKeys = false
		return m, nil
	}
	if m.picking && key != "ctrl+c" {
		return m.pickKey(k)
	}
	typing := m.focus == focusNew || m.focus == focusAsk || m.searching
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "alt+w":
		return m.openPicker()
	case "?":
		if !typing {
			m.showKeys = true
			return m, nil
		}
	case "1", "2", "3", "4", "5":
		if typing {
			break
		}
		fallthrough
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5":
		t := tab(key[len(key)-1] - '1')
		if m.settings.tabHidden(t) {
			return m, nil
		}
		m = m.switchTab(t)
		switch {
		case m.tab == tabSettings:
			return m, tea.Batch(statesCmd(m.issues), prsCmd(m.settings.ReviewLabels))
		case m.tab == tabReviews && time.Since(m.prsAt) > time.Minute:
			m.prsLoading = true
			return m, prsCmd(m.settings.ReviewLabels)
		case m.tab == tabDaily && !m.dailyBusy && m.dailyAt.Format(time.DateOnly) != time.Now().Format(time.DateOnly):
			m.dailyBusy = true
			return m, dailyCmd(false, m.settings.language())
		}
		return m, nil
	}
	if m.tab == tabSettings {
		return m.settingsKey(key)
	}
	if m.tab == tabDaily {
		return m.dailyKey(k)
	}
	confirm := m.pending
	m.pending = ""
	if m.tab == tabReviews && m.focus != focusNew {
		return m.reviewsKey(k, confirm)
	}
	switch {
	case m.focus == focusNew:
		switch key {
		case "esc":
			m.focus, m.newIn = focusStrip, ""
			return m, nil
		case "ctrl+s", "alt+enter":
			return m.submit()
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(k)
		return m, cmd
	case m.focus == focusStrip:
		return m.stripKey(key, confirm)
	case m.searching:
		switch key {
		case "esc":
			m.search.SetValue("")
			fallthrough
		case "enter":
			m.searching = false
			m.search.Blur()
			m.buildColumns()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(k)
		m.buildColumns()
		return m, cmd
	}
	switch key {
	case "left", "h":
		m.col--
	case "right", "l":
		m.col++
	case "up", "k":
		m.row--
	case "down", "j":
		m.row++
	case "enter":
		return m.openTicket(false)
	case "n":
		m.newIn = ""
		if i := m.selectedIssue(); i != nil && len(m.collapsed[i.Identifier].issues) > 1 {
			m.newIn = m.collapsed[i.Identifier].ID
		}
		return m.openTicket(true)
	case "z", "Z":
		return m.foldKey(key)
	case "g", "G":
		if i := m.selectedIssue(); i != nil {
			m.marked = toggle(m.marked, i.Identifier)
		}
	case "p":
		if i := m.selectedIssue(); i != nil {
			m.openPRs(m.cardKeys(*i)...)
		}
	case "tab", "alt+down":
		if m.ticket != nil {
			m.focus = focusStrip
		}
	case "esc":
		if len(m.marked) > 0 {
			m.marked = nil
			break
		}
		m.closeTicket()
	case "/":
		m.searching = true
		return m, m.search.Focus()
	case "r":
		m.loading = true
		return m, loadBoardCmd(m.settings.sources())
	case "o":
		if i := m.selectedIssue(); i != nil {
			openURL(i.URL)
		}
	case "q":
		return m, tea.Quit
	}
	m.clamp()
	return m, nil
}

func (m model) stripKey(key, confirm string) (model, tea.Cmd) {
	key0, list := m.listKey(), m.listSessions()
	prs := m.prsFor(key0)
	var s *Session
	pr := ""
	switch i := m.sel - len(prs); {
	case m.sel >= 0 && m.sel < len(prs):
		pr = prs[m.sel]
	case i >= 0 && i < len(list):
		s = &list[i]
	}
	pane, live := "", false
	if s != nil {
		pane, live = m.live[s.ID]
	}
	inTicket := m.tab == tabKanban && m.ticket != nil
	switch key {
	case "q":
		return m, tea.Quit
	case "esc", "tab", "alt+up":
		if m.tab == tabKanban || m.tab == tabReviews {
			m.focus = focusBoard
		}
	case "up", "k":
		m.sel = max(0, m.sel-1)
	case "down", "j":
		m.sel = max(0, min(len(prs)+len(list)-1, m.sel+1))
	case "pgdown", "ctrl+d", " ":
		m.vp.HalfPageDown()
	case "pgup", "ctrl+u":
		m.vp.HalfPageUp()
	case "n":
		return m, m.startNew()
	case "p":
		if inTicket {
			m.openPRs(m.ticket.Identifier)
		}
	case "o":
		if inTicket {
			openURL(m.ticket.URL)
		}
	case "r":
		if inTicket {
			return m, loadDetailCmd(m.ticket.Identifier)
		}
	case "enter":
		if pr != "" {
			openURL(pr)
		}
		if s == nil {
			break
		}
		if !live {
			return m, resumeCmd(s.ID, m.titleOf(s.ID), m.ktaArgs(m.sessionWork(s.ID)))
		}
		if err := showRight(pane); err != nil {
			m.err = err
		} else {
			focusPane(pane)
		}
	case "x":
		if !live {
			break
		}
		if confirm != "x"+s.ID {
			m.pending = "x" + s.ID
			break
		}
		if err := killPane(pane); err != nil {
			m.err = err
		}
	case "d":
		if s == nil {
			break
		}
		if confirm != "d"+s.ID {
			m.pending = "d" + s.ID
			break
		}
		if live {
			if err := killPane(pane); err != nil {
				m.err = err
				break
			}
		}
		m.removeSession(key0, s.ID)
	}
	return m, nil
}

type keyHelp struct {
	key, desc string
	short     bool
}

func (m model) keyHelp() []keyHelp {
	tabs := keyHelp{"alt+1…5", "switch tabs", false}
	claude := keyHelp{"alt+→", "go to the Claude session", false}
	quit := keyHelp{"q", "quit", false}
	stop := keyHelp{"x x", "stop the session", false}
	remove := keyHelp{"d d", "remove the session", false}
	switch {
	case m.picking:
		return []keyHelp{{"enter", "open", true}, {"↑↓", "move", true}, {"esc", "close", true}}
	case m.tab == tabReviews:
		return m.reviewKeys()
	case m.tab == tabDaily:
		return []keyHelp{{"r", "regenerate", true}, {"↑↓", "scroll", false}, tabs, quit}
	case m.tab == tabSettings:
		return []keyHelp{{"space", "toggle", true}, {"←→", "change language", true}, {"↑↓", "move", false}, tabs, quit}
	case m.focus == focusNew:
		return []keyHelp{{"ctrl+s", "start the session", true}, {"esc", "cancel", true}}
	case m.searching:
		return []keyHelp{{"enter", "keep the search", true}, {"esc", "clear", true}}
	case m.tab == tabSessions:
		return []keyHelp{{"enter", "show", true}, {"n", "new session", true}, {"↑↓", "select", false}, stop, remove, claude, tabs, quit}
	case m.focus == focusStrip:
		return []keyHelp{{"enter", "show / open PR", true}, {"n", "new session", true}, {"p", "open PRs", true}, {"↑↓", "select", false}, {"o", "open in browser", false}, stop, remove, {"pgup/pgdn", "scroll the ticket", false}, {"r", "reload the ticket", false}, {"alt+↑ / esc", "back to the kanban", false}, claude, tabs, quit}
	}
	marks := len(m.marked)
	keys := []keyHelp{{"enter", "open", marks == 0}, {"n", "new session", marks == 0}, {"/", "search", marks == 0}, {"←→↑↓", "move", false}, {"g", "mark cards to work on together", false}, {"z / Z", "fold or unfold the group / all groups", false}, {"p", "open PRs", false}, {"o", "open in browser", false}, {"r", "refresh", false}}
	if marks > 0 {
		group := []keyHelp{{"g", "mark / unmark", true}}
		if marks > 1 {
			group = append(group, keyHelp{"n", fmt.Sprintf("one session for the %d cards", marks), true})
		}
		keys = append(append(group, keyHelp{"esc", "clear the marks", true}), keys...)
	}
	if m.ticket != nil {
		keys = append(keys, keyHelp{"alt+↓ / tab", "ticket panel", false}, keyHelp{"esc", "close the ticket", false}, claude)
	}
	return append(keys, keyHelp{"alt+w", "workspaces", false}, tabs, quit)
}
