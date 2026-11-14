package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type (
	prsMsg struct {
		prs    []PR
		labels []string
		err    error
	}
	reviewMsg struct {
		url    string
		review *Review
		err    error
	}
	askMsg struct {
		url, threadID, kind, prompt, answer, sessionID string
		err                                            error
	}
	sentMsg struct {
		url string
		err error
	}
)

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (m model) currentReview() *Review {
	if m.openPR == nil {
		return nil
	}
	return m.reviews[m.openPR.URL]
}

func (m model) curThread() *Thread {
	r := m.currentReview()
	if r == nil || len(r.Threads) == 0 {
		return nil
	}
	return &r.Threads[max(0, min(m.thread, len(r.Threads)-1))]
}

func (m model) prStatus(p PR) string {
	var parts []string
	switch p.CI {
	case "SUCCESS":
		parts = append(parts, stateStyle["working"].Render("✓ CI"))
	case "FAILURE", "ERROR":
		parts = append(parts, errStyle.Render("✗ CI"))
	case "PENDING", "EXPECTED":
		parts = append(parts, stateStyle["waiting"].Render("… CI"))
	}
	if p.Mine {
		switch p.Decision {
		case "APPROVED":
			parts = append(parts, stateStyle["working"].Render("approved"))
		case "CHANGES_REQUESTED":
			parts = append(parts, errStyle.Render("changes requested"))
		case "REVIEW_REQUIRED":
			parts = append(parts, dim.Render("review required"))
		}
		if n := len(p.Threads); n > 0 {
			parts = append(parts, stateStyle["waiting"].Render(fmt.Sprintf("%d open", n)))
		}
		return strings.Join(parts, dim.Render(" · "))
	}
	r := m.reviews[p.URL]
	switch {
	case m.generating[p.URL]:
		parts = append(parts, stateStyle["background"].Render("reviewing…"))
	case r != nil && r.SentAt != "":
		parts = append(parts, dim.Render("sent"))
	case r != nil:
		parts = append(parts, prStyle.Render(plural(len(r.Threads), "thread")))
		if r.Skipped > 0 {
			parts = append(parts, dim.Render(fmt.Sprintf("%d outside the diff, dropped", r.Skipped)))
		}
	}
	return strings.Join(parts, dim.Render(" · "))
}

func (m model) prListView(height int) string {
	w := m.inner()
	if m.prs == nil {
		if m.prsLoading {
			return dim.Render("loading your pull requests…")
		}
		return dim.Render("press r to load your pull requests")
	}
	var lines []string
	at := 0
	row := func(i int, p PR) {
		cursor := "  "
		if i == m.prSel {
			cursor, at = lipgloss.NewStyle().Foreground(accent).Render("▸ "), len(lines)
		}
		left := cursor + prStyle.Render(prTag(p.URL)) + " " + p.Title
		if !p.Mine {
			left += dim.Render("  @" + p.Author)
		}
		if b := m.badge(prKey(p)); b != "" {
			left += " " + b
		}
		status := m.prStatus(p)
		left = ansi.Truncate(left, max(10, w-lipgloss.Width(status)-2), "…")
		lines = append(lines, left+strings.Repeat(" ", max(2, w-lipgloss.Width(left)-lipgloss.Width(status)))+status)
	}
	lines = append(lines, bold.Render("Mine"))
	team := 0
	for i, p := range m.prs {
		if p.Mine {
			row(i, p)
		} else {
			team++
		}
	}
	if len(m.prs) == team {
		lines = append(lines, dim.Render("  no open pull requests"))
	}
	head := bold.Render("Team")
	if len(m.settings.ReviewLabels) > 0 {
		head += dim.Render(" · " + strings.Join(m.settings.ReviewLabels, ", "))
	}
	lines = append(lines, "", head)
	switch {
	case len(m.settings.ReviewLabels) == 0:
		lines = append(lines, dim.Render("  pick your team's labels in settings (5)"))
	case team == 0:
		lines = append(lines, dim.Render("  no open pull requests with these labels"))
	}
	for i, p := range m.prs {
		if !p.Mine {
			row(i, p)
		}
	}
	return strings.Join(lines[max(0, at-height+2):], "\n")
}

func (m model) prPanelTop() string {
	w, p := m.inner(), m.openPR
	head := bold.Render(prKey(*p)) + dim.Render(" · ") + p.Title
	if !p.Mine {
		head += dim.Render("  @" + p.Author)
	}
	lines := []string{ansi.Truncate(head+"  "+m.prStatus(*p), w, "…")}
	if p.Mine {
		lines = append(lines, m.sessionLines()...)
	} else {
		lines = append(lines, m.reviewStatus())
	}
	return strings.Join(append(lines, dim.Render(strings.Repeat("─", w))), "\n")
}

func (m model) reviewStatus() string {
	p, r, t := m.openPR, m.currentReview(), m.curThread()
	switch {
	case m.pending == "r"+p.URL:
		return errStyle.Render("press r again to review it again (replaces these threads)")
	case m.pending == "s"+p.URL:
		return errStyle.Render(fmt.Sprintf("press s again to send %s to GitHub as a draft review", plural(len(r.Threads), "thread")))
	case t != nil && m.pending == "x"+t.ID:
		return errStyle.Render("press x again to discard this thread")
	case m.generating[p.URL]:
		return stateStyle["background"].Render("Claude is reviewing this PR, it can take a few minutes…")
	case m.sending[p.URL]:
		return stateStyle["background"].Render("sending the draft review…")
	case r == nil:
		return dim.Render("r to review this PR with Claude")
	case t == nil:
		return dim.Render("no threads · r r to review it again")
	}
	line := fmt.Sprintf("thread %d/%d · %s L%d", m.thread+1, len(r.Threads), t.File, t.HighlightStart)
	if t.HighlightEnd != t.HighlightStart {
		line += fmt.Sprintf("–%d", t.HighlightEnd)
	}
	if r.SentAt != "" {
		if at, err := time.Parse(time.RFC3339, r.SentAt); err == nil {
			line += dim.Render("  · sent as draft " + at.Local().Format("02/01 15:04"))
		}
	}
	return line
}

func (m model) prPanelBody() string {
	w, p := m.inner(), m.openPR
	wrap := func(s string) string { return ansi.Wordwrap(s, w-2, "") }
	if p.Mine {
		if len(p.Threads) == 0 {
			return dim.Render("no open review threads")
		}
		lines := []string{bold.Render(fmt.Sprintf("Open threads (%d)", len(p.Threads)))}
		for _, t := range p.Threads {
			lines = append(lines, "", dim.Render(fmt.Sprintf("%s:%d", t.Path, t.Line))+"  @"+t.Author, wrap(t.Body))
		}
		return strings.Join(lines, "\n")
	}
	t := m.curThread()
	if t == nil {
		return ""
	}
	var lines []string
	for i, l := range strings.Split(t.Code, "\n") {
		n, mark := t.StartLine+i, dim.Render("│")
		if n >= t.HighlightStart && n <= t.HighlightEnd {
			mark = lipgloss.NewStyle().Foreground(focusColor).Render("▌")
		}
		lines = append(lines, ansi.Truncate(dim.Render(fmt.Sprintf("%4d ", n))+mark+" "+strings.ReplaceAll(l, "\t", "  "), w, "…"))
	}
	chat := func(key string) {
		for _, c := range m.currentReview().Chats[key] {
			who := dim.Render("› you: ")
			if c.Role != "you" {
				who = lipgloss.NewStyle().Foreground(accent).Render("› claude: ")
			}
			lines = append(lines, wrap(who+c.Text))
		}
		if m.busy[p.URL+"|"+key] {
			lines = append(lines, lipgloss.NewStyle().Foreground(accent).Render("› claude: ")+dim.Render("thinking…"))
		}
	}
	lines = append(lines, "", bold.Render("Why"), wrap(t.Explanation))
	chat(chatKey(t.ID, "explanation"))
	lines = append(lines, "", bold.Render("Comment"), prStyle.Render(wrap(t.Comment)))
	chat(chatKey(t.ID, "comment"))
	if key := chatKey("", "general"); len(m.currentReview().Chats[key]) > 0 || m.busy[p.URL+"|"+key] {
		lines = append(lines, "", bold.Render("About the review"))
		chat(key)
	}
	return strings.Join(lines, "\n")
}

func (m model) askLine() string {
	if m.focus != focusAsk {
		return ""
	}
	label := map[string]string{"explanation": "ask about the why", "comment": "change the comment", "general": "ask about the review"}[m.askKind]
	return "\n" + lipgloss.NewStyle().Foreground(accent).Render(label+" › ") + m.ask.View()
}

func (m *model) fitReviews() {
	w := m.inner()
	m.ta.SetWidth(w)
	m.ta.SetHeight(5)
	m.ask.Width = max(10, w-25)
	if m.openPR == nil {
		return
	}
	askH := 0
	if m.focus == focusAsk {
		askH = 1
	}
	m.rvp.Width = w
	m.rvp.Height = max(1, m.stripHeight()-2-lipgloss.Height(m.prPanelTop())-askH)
	m.rvp.SetContent(m.prPanelBody())
	key := fmt.Sprintf("%s|%d|%d", m.openPR.URL, m.thread, w)
	if key != m.rvpKey {
		m.rvp.GotoTop()
		m.rvpKey = key
	}
}

func (m model) reviewsView() string {
	stripH := 0
	if m.openPR != nil {
		stripH = m.stripHeight()
	}
	listH := m.h - 2 - stripH
	body := m.box("reviews", m.focus == focusBoard, listH-2, m.prListView(listH-2))
	if m.openPR != nil {
		body += "\n" + m.box(prKey(*m.openPR), m.focus != focusBoard, stripH-2, m.prPanelTop()+"\n"+m.rvp.View()+m.askLine())
	}
	return body
}

func (m model) openPRPanel(p PR) model {
	m.openPR, m.thread, m.sel, m.focus = &p, 0, 0, focusStrip
	pane := ""
	if p.Mine {
		pane = m.bestPane(prKey(p))
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

func (m model) reviewsKey(k tea.KeyMsg, confirm string) (model, tea.Cmd) {
	key := k.String()
	switch {
	case m.focus == focusAsk:
		switch key {
		case "esc":
			m.focus = focusStrip
			return m, nil
		case "enter":
			prompt, r := strings.TrimSpace(m.ask.Value()), m.currentReview()
			m.focus = focusStrip
			if prompt == "" || r == nil {
				return m, nil
			}
			threadID := ""
			if t := m.curThread(); m.askKind != "general" && t != nil {
				threadID = t.ID
			}
			m.busy[r.PRURL+"|"+chatKey(threadID, m.askKind)] = true
			return m, askCmd(*r, threadID, m.askKind, prompt)
		}
		var cmd tea.Cmd
		m.ask, cmd = m.ask.Update(k)
		return m, cmd
	case m.focus == focusBoard:
		return m.prListKey(key)
	case m.openPR != nil && m.openPR.Mine:
		if key == "o" {
			openURL(m.openPR.URL)
			return m, nil
		}
		return m.stripKey(key, confirm)
	case m.openPR != nil:
		return m.teamKey(key, confirm)
	}
	return m, nil
}

func (m model) prListKey(key string) (model, tea.Cmd) {
	var sel *PR
	if m.prSel < len(m.prs) {
		sel = &m.prs[m.prSel]
	}
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.prSel = max(0, m.prSel-1)
	case "down", "j":
		m.prSel = max(0, min(len(m.prs)-1, m.prSel+1))
	case "enter":
		if sel != nil {
			return m.openPRPanel(*sel), nil
		}
	case "o":
		if sel != nil {
			openURL(sel.URL)
		}
	case "r":
		m.prsLoading = true
		return m, prsCmd(m.settings.ReviewLabels)
	case "tab", "alt+down":
		if m.openPR != nil {
			m.focus = focusStrip
		}
	case "esc":
		if err := hideRight(); err != nil {
			m.err = err
		}
		m.openPR = nil
	}
	return m, nil
}

func (m model) teamKey(key, confirm string) (model, tea.Cmd) {
	p, r, t := *m.openPR, m.currentReview(), m.curThread()
	switch key {
	case "q":
		return m, tea.Quit
	case "esc", "tab", "alt+up":
		m.focus = focusBoard
	case "left", "h":
		m.thread = max(0, m.thread-1)
	case "right", "l":
		if r != nil {
			m.thread = max(0, min(len(r.Threads)-1, m.thread+1))
		}
	case "up", "k":
		m.rvp.LineUp(1)
	case "down", "j":
		m.rvp.LineDown(1)
	case "pgup", "ctrl+u":
		m.rvp.HalfPageUp()
	case "pgdown", "ctrl+d", " ":
		m.rvp.HalfPageDown()
	case "o":
		openURL(p.URL)
	case "r":
		if m.generating[p.URL] || slices.ContainsFunc(slices.Collect(maps.Keys(m.busy)), func(k string) bool { return strings.HasPrefix(k, p.URL+"|") }) {
			break
		}
		if r != nil && confirm != "r"+p.URL {
			m.pending = "r" + p.URL
			break
		}
		m.generating[p.URL] = true
		return m, generateCmd(p, m.settings.language())
	case "a", "c", "g":
		kind := map[string]string{"a": "explanation", "c": "comment", "g": "general"}[key]
		if r == nil || (t == nil && kind != "general") || m.generating[p.URL] {
			break
		}
		m.askKind, m.focus = kind, focusAsk
		m.ask.Reset()
		return m, m.ask.Focus()
	case "x":
		if t == nil {
			break
		}
		if confirm != "x"+t.ID {
			m.pending = "x" + t.ID
			break
		}
		r.Threads = slices.DeleteFunc(r.Threads, func(x Thread) bool { return x.ID == t.ID })
		if err := saveReview(r); err != nil {
			m.err = err
		}
		m.thread = max(0, min(m.thread, len(r.Threads)-1))
	case "s":
		if r == nil || len(r.Threads) == 0 || m.sending[p.URL] {
			break
		}
		if confirm != "s"+p.URL {
			m.pending = "s" + p.URL
			break
		}
		m.sending[p.URL] = true
		return m, sendCmd(p, *r)
	}
	return m, nil
}

func (m model) updateReviews(msg tea.Msg) model {
	switch msg := msg.(type) {
	case prsMsg:
		m.prsLoading, m.prsAt = false, time.Now()
		if msg.err != nil && m.tab == tabReviews {
			m.err = msg.err
		}
		if msg.prs == nil && msg.err != nil {
			return m
		}
		m.prs, m.labelPool = msg.prs, msg.labels
		if m.prs == nil {
			m.prs = []PR{}
		}
		for _, p := range m.prs {
			if _, ok := m.reviews[p.URL]; !ok && !p.Mine {
				if r, err := loadReview(p.URL); err == nil && r != nil {
					m.reviews[p.URL] = r
				}
			}
			if m.openPR != nil && p.URL == m.openPR.URL {
				cp := p
				m.openPR = &cp
			}
		}
		m.prSel = max(0, min(m.prSel, len(m.prs)-1))
	case reviewMsg:
		delete(m.generating, msg.url)
		if msg.err != nil {
			m.err = msg.err
			return m
		}
		if err := saveReview(msg.review); err != nil {
			m.err = err
			return m
		}
		m.reviews[msg.url] = msg.review
		if m.openPR != nil && m.openPR.URL == msg.url {
			m.thread = 0
		}
	case askMsg:
		delete(m.busy, msg.url+"|"+chatKey(msg.threadID, msg.kind))
		if r := m.reviews[msg.url]; r != nil {
			applyAnswer(r, msg)
			if err := saveReview(r); err != nil {
				m.err = err
			}
		}
		if msg.err != nil {
			m.err = msg.err
		}
	case sentMsg:
		delete(m.sending, msg.url)
		if msg.err != nil {
			m.err = msg.err
			return m
		}
		if r := m.reviews[msg.url]; r != nil {
			r.SentAt = time.Now().Format(time.RFC3339)
			if err := saveReview(r); err != nil {
				m.err = err
			}
		}
	}
	return m
}

func (m model) reviewKeys() []keyHelp {
	tabs := keyHelp{"alt+1…5", "switch tabs", false}
	quit := keyHelp{"q", "quit", false}
	switch {
	case m.focus == focusAsk:
		return []keyHelp{{"enter", "send", true}, {"esc", "cancel", true}}
	case m.focus == focusNew:
		return []keyHelp{{"ctrl+s", "start the session", true}, {"esc", "cancel", true}}
	case m.focus == focusBoard || m.openPR == nil:
		keys := []keyHelp{{"enter", "open", true}, {"o", "open in browser", true}, {"r", "refresh", true}, {"↑↓", "select", false}}
		if m.openPR != nil {
			keys = append(keys, keyHelp{"alt+↓ / tab", "PR panel", false}, keyHelp{"esc", "close the PR", false})
		}
		return append(keys, tabs, quit)
	case m.openPR.Mine:
		return []keyHelp{{"enter", "show", true}, {"n", "new session", true}, {"o", "open in browser", true}, {"↑↓", "select", false}, {"x x", "stop the session", false}, {"d d", "remove the session", false}, {"alt+↑ / esc", "back to the list", false}, {"alt+→", "go to the Claude session", false}, tabs, quit}
	}
	return []keyHelp{{"←→", "thread", true}, {"a", "ask about the why", true}, {"c", "change the comment", true}, {"s s", "send as draft", true}, {"r", "review with Claude (r r again)", false}, {"g", "ask about the whole review", false}, {"x x", "discard the thread", false}, {"o", "open in browser", false}, {"↑↓ pgup/pgdn", "scroll", false}, {"alt+↑ / esc", "back to the list", false}, tabs, quit}
}
