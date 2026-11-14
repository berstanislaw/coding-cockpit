package ui

import (
	"fmt"
	"kta/internal/config"
	"kta/internal/daily"
	"kta/internal/github"
	"kta/internal/store"
	"kta/internal/tracker"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func groupColor(g store.Group) lipgloss.Color { return hueColor(float64(g.Hue)) }

func hueColor(h float64) lipgloss.Color {
	s, l := 0.7, 0.68
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	o := l - c/2
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", int((r+o)*255), int((g+o)*255), int((b+o)*255)))
}

func marquee(s string, width, frame int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	loop := []rune(s + "   ·   ")
	start := frame % len(loop)
	return ansi.Truncate(string(append(loop[start:], loop[:start]...)), width, "")
}

func (m *model) fitDaily() {
	w := m.inner()
	m.dailyVP.Width, m.dailyVP.Height = w, max(1, m.h-5)
	if m.dailyW != w {
		m.dailyVP.SetContent(renderMarkdown(m.daily, w))
		m.dailyW = w
	}
}

func (m model) dailyTop() string {
	since := daily.Since(time.Now()).Format("Mon 02/01")
	switch {
	case m.dailyBusy:
		return dim.Render("since " + since + " · summarizing your Claude sessions…")
	case m.dailyAt.IsZero():
		return ""
	}
	return dim.Render("since " + since + " · generated " + m.dailyAt.Format("15:04"))
}

func issueMarkdown(i tracker.Issue) string {
	var b strings.Builder
	if i.Description == "" {
		b.WriteString("_no description_")
	}
	b.WriteString(i.Description)
	for _, c := range i.Comments.Nodes {
		name := "integration"
		if c.User != nil {
			name = c.User.Name
		}
		fmt.Fprintf(&b, "\n\n---\n\n**%s** · %.10s\n\n%s", name, c.CreatedAt, c.Body)
	}
	return b.String()
}

func renderMarkdown(raw string, width int) string {
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, width-4)))
	if err != nil {
		return raw
	}
	out, err := r.Render(raw)
	if err != nil {
		return raw
	}
	return out
}

func (m model) stripHeight() int {
	return max(10, min(m.h*45/100, m.h-2-9))
}

func (m model) inner() int {
	return max(10, m.w-2)
}

func (m *model) fitViewport() {
	w := m.inner()
	m.ta.SetWidth(w)
	m.ta.SetHeight(5)
	m.vp.Width = w
	m.vp.Height = max(1, m.stripHeight()-2-lipgloss.Height(m.stripTop()))
	if m.mdRaw == "" {
		m.vp.SetContent(dim.Render("loading…"))
	} else if m.mdWidth != w {
		m.vp.SetContent(renderMarkdown(m.mdRaw, w))
		m.mdWidth = w
	}
}

func (m model) colWindow() (first, fit, cw int) {
	w := m.inner()
	n := len(m.cols)
	fit = max(1, min(n, (w+1)/27))
	cw = min(44, (w-(fit-1))/fit)
	if m.col >= fit {
		first = m.col - fit + 1
	}
	return first, fit, cw
}

func (m model) box(title string, focused bool, height int, content string) string {
	w := m.inner()
	border, color, label := lipgloss.RoundedBorder(), lipgloss.Color("#3a4050"), dim.Render(" "+title+" ")
	if focused && m.paneFocused {
		border, color = lipgloss.ThickBorder(), focusColor
		label = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(accent).Render(" ● " + title + " ")
	}
	edge := lipgloss.NewStyle().Foreground(color)
	top := edge.Render(border.TopLeft+border.Top) + label + edge.Render(strings.Repeat(border.Top, max(0, w-1-lipgloss.Width(label)))+border.TopRight)
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}
	return top + "\n" + lipgloss.NewStyle().Border(border, false, true, true, true).BorderForeground(color).Width(w).Render(strings.Join(lines, "\n"))
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	var body string
	switch {
	case m.picking:
		body = m.workspacesView(m.h - 2)
	case m.showKeys:
		body = m.box("keys", true, m.h-4, m.keysView())
	case m.tab == tabSessions:
		body = m.box("sessions", true, m.h-4, strings.Join(m.sessionLines(), "\n"))
	case m.tab == tabReviews:
		body = m.reviewsView()
	case m.tab == tabDaily:
		body = m.box("daily", true, m.h-4, m.dailyTop()+"\n"+m.dailyVP.View())
	case m.tab == tabSettings:
		body = m.box("settings", true, m.h-4, m.settingsView(m.h-4))
	default:
		stripH := 0
		if m.ticket != nil {
			stripH = m.stripHeight()
		}
		boardH := m.h - 2 - stripH
		body = m.box("kanban", m.focus == focusBoard, boardH-2, m.viewColumns(boardH-2))
		if m.ticket != nil {
			body += "\n" + m.box("ticket", m.focus != focusBoard, stripH-2, m.stripTop()+"\n"+m.vp.View())
		}
	}
	return strings.Join([]string{m.header(), body, m.footer()}, "\n")
}

func (m model) footer() string {
	if m.showKeys {
		return dim.Render("any key closes")
	}
	var parts []string
	for _, k := range m.keyHelp() {
		if k.short {
			parts = append(parts, k.key+" "+k.desc)
		}
	}
	if m.focus != focusNew && m.focus != focusAsk && !m.searching && !m.picking {
		parts = append(parts, "? keys")
	}
	return dim.Render(ansi.Truncate(strings.Join(parts, " · "), m.w, "…"))
}

func (m model) keysView() string {
	lines := []string{""}
	for _, k := range m.keyHelp() {
		lines = append(lines, "  "+bold.Render(k.key)+strings.Repeat(" ", max(2, 14-lipgloss.Width(k.key)))+k.desc)
	}
	return strings.Join(lines, "\n")
}

func (m model) header() string {
	header := ""
	if config.Workspace != "" {
		header = lipgloss.NewStyle().Bold(true).Foreground(accent).Render(config.Workspace) + " "
	}
	for i, name := range tabNames {
		if tabHidden(m.settings, tab(i)) {
			continue
		}
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if tab(i) == m.tab {
			header += lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(accent).Render(label)
		} else {
			header += dim.Render(label)
		}
		if b := m.badge(store.FreeKey); tab(i) == tabSessions && b != "" {
			header += b + " "
		}
	}
	if m.tab == tabKanban {
		header += "  "
		if m.searching || m.search.Value() != "" {
			header += m.search.View()
		} else {
			header += dim.Render("/ search")
		}
		if n := len(m.marked); n > 0 {
			header += markStyle.Render(fmt.Sprintf("  ✓ %d marked", n))
		}
		if first, fit, _ := m.colWindow(); fit < len(m.cols) {
			header += dim.Render(fmt.Sprintf("  columns %d-%d of %d", first+1, first+fit, len(m.cols)))
		}
	}
	if m.loading {
		header += dim.Render("  loading…")
	}
	if m.err != nil {
		header += "  " + errStyle.Render(strings.Join(strings.Fields(m.err.Error()), " "))
	}
	return ansi.Truncate(header, m.w, "…")
}

func (m model) viewColumns(height int) string {
	n := len(m.cols)
	if n == 0 {
		if m.loading {
			return ""
		}
		return dim.Render("no issues")
	}
	first, fit, cw := m.colWindow()
	cardH := func(i tracker.Issue) int { return lipgloss.Height(m.card(i, cw-1, false)) }
	render := func(i tracker.Issue, cursor bool) string {
		c := m.card(i, cw-1, cursor)
		bar := " "
		if g := m.groupOf(i.Identifier); g != nil {
			bar = lipgloss.NewStyle().Foreground(groupColor(*g)).Render("┃")
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, strings.TrimSuffix(strings.Repeat(bar+"\n", lipgloss.Height(c)), "\n"), c)
	}
	var blocks []string
	for c := first; c < min(n, first+fit); c++ {
		col := m.cols[c]
		count := len(col.items)
		for _, i := range col.items {
			count += max(0, len(m.collapsed[i.Identifier].issues)-1)
		}
		head := lipgloss.NewStyle().Foreground(lipgloss.Color(col.state.Color)).Render("●") + " " + bold.Render(col.state.Name) + dim.Render(fmt.Sprintf(" %d", count))
		lines := []string{ansi.Truncate(head, cw, "…")}
		offset := 0
		if c == m.col {
			need := 0
			for r := 0; r <= m.row && r < len(col.items); r++ {
				need += cardH(col.items[r])
			}
			for offset < m.row && need > height-1 {
				need -= cardH(col.items[offset])
				offset++
			}
		}
		used := 0
		for r := offset; r < len(col.items); r++ {
			if used += cardH(col.items[r]); used > height-1 && r > offset {
				break
			}
			lines = append(lines, render(col.items[r], c == m.col && r == m.row))
		}
		if c > first {
			blocks = append(blocks, " ")
		}
		blocks = append(blocks, lipgloss.NewStyle().Width(cw).Render(strings.Join(lines, "\n")))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

func (m model) card(i tracker.Issue, cw int, cursor bool) string {
	inner := cw - 4
	marked := slices.Contains(m.marked, i.Identifier)
	meta := i.Identifier
	if marked {
		meta = markStyle.Render("✓ " + i.Identifier)
	}
	if p := int(i.Priority); p > 0 && p < len(priorities) {
		meta += " " + prioStyle[p].Render(priorities[p])
	}
	if b := m.badge(m.sessionKeys(i.Identifier)...); b != "" {
		meta += " " + b
	}
	if g := m.groupOf(i.Identifier); g != nil {
		meta += " " + lipgloss.NewStyle().Foreground(groupColor(*g)).Render("◆")
	}
	var mate lipgloss.Color
	if sel := m.selectedIssue(); sel != nil && sel.Identifier != i.Identifier {
		if g := m.groupOf(sel.Identifier); g != nil && slices.Contains(g.Tickets, i.Identifier) {
			mate = groupColor(*g)
		}
	}
	body := clampLines(i.Title, inner, 2)
	if prs := m.prsFor(i.Identifier); len(prs) > 0 {
		tags := make([]string, len(prs))
		for k, url := range prs {
			tags[k] = prStyle.Render(github.PRTag(url))
		}
		titleLines := min(2, len(strings.Split(ansi.Wordwrap(i.Title, inner, ""), "\n")))
		body = clampLines(i.Title, inner, titleLines) + "\n" + ansi.Truncate(strings.Join(tags, " "), inner, "…")
	}
	if cg := m.collapsed[i.Identifier]; len(cg.issues) > 1 {
		ids := make([]string, len(cg.issues))
		for k, x := range cg.issues {
			ids[k] = x.Identifier
		}
		meta = lipgloss.NewStyle().Foreground(groupColor(cg.Group)).Bold(true).Render(fmt.Sprintf("◆ %d cards", len(cg.issues)))
		if b := m.badge(append(slices.Clone(ids), store.GroupKey(cg.ID))...); b != "" {
			meta += " " + b
		}
		shown := cg.issues[m.frame/12%len(cg.issues)]
		body = marquee(strings.Join(ids, " · "), inner, m.frame) + "\n" + ansi.Truncate(lipgloss.NewStyle().Foreground(groupColor(cg.Group)).Render(shown.Identifier)+" "+dim.Render(shown.Title), inner, "…")
	}
	style := cardStyle
	switch {
	case cursor && m.focus == focusBoard && m.paneFocused:
		style = style.Border(lipgloss.ThickBorder()).BorderForeground(focusColor)
	case m.ticket != nil && m.ticket.Identifier == i.Identifier:
		style = style.BorderForeground(openColor)
	case marked:
		style = style.Border(lipgloss.DoubleBorder()).BorderForeground(markColor)
	case mate != "":
		style = style.BorderForeground(mate)
	case cursor:
		style = style.BorderForeground(lipgloss.Color("#8b919c"))
	}
	return style.Width(cw - 2).Render(ansi.Truncate(meta, inner, "…") + "\n" + body)
}

func (m model) badge(keys ...string) string {
	count := map[string]int{}
	seen := map[string]bool{}
	for _, key := range keys {
		for _, s := range m.sessions[key] {
			if !seen[s.ID] {
				seen[s.ID] = true
				count[m.sessionState(s.ID)]++
			}
		}
	}
	var out []string
	for _, st := range []string{"waiting", "working", "background", "stopped"} {
		switch n := count[st]; {
		case n == 1:
			out = append(out, stateIcon[st])
		case n > 1:
			out = append(out, stateStyle[st].Render(fmt.Sprintf("%s%d", stateGlyph[st], n)))
		}
	}
	return strings.Join(out, " ")
}

func clampLines(s string, width, n int) string {
	lines := strings.Split(ansi.Wordwrap(s, width, ""), "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines[n-1] = ansi.Truncate(strings.TrimRight(lines[n-1], " "), width-1, "") + "…"
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m model) stripTop() string {
	w := m.inner()
	i := m.ticket
	head := bold.Render(i.Identifier) + dim.Render(" · "+i.State.Name)
	if p := int(i.Priority); p > 0 && p < len(priorities) {
		head += dim.Render(" · ") + prioStyle[p].Render(priorities[p])
	}
	lines := append([]string{ansi.Truncate(head+"  "+i.Title, w, "…")}, m.sessionLines()...)
	return strings.Join(append(lines, dim.Render(strings.Repeat("─", w))), "\n")
}

func (m model) sessionLines() []string {
	if m.focus == focusNew {
		if keys := m.groupKeys(); keys != nil {
			return []string{bold.Render("New session for ") + markStyle.Render(strings.Join(keys, ", ")), m.ta.View()}
		}
		return []string{bold.Render("New session"), m.ta.View()}
	}
	w := m.inner()
	list := m.listSessions()
	prs := m.prsFor(m.listKey())
	cursorAt := func(idx int) string {
		if idx == m.sel && m.focus == focusStrip {
			return lipgloss.NewStyle().Foreground(accent).Render("▸ ")
		}
		return "  "
	}
	var lines []string
	for idx, url := range prs {
		lines = append(lines, ansi.Truncate(cursorAt(idx)+prStyle.Render("PR")+" "+github.PRLabel(url), w, "…"))
	}
	if len(list) == 0 {
		lines = append(lines, dim.Render("no sessions · n to start one"))
	}
	for idx, s := range list {
		cursor := cursorAt(len(prs) + idx)
		state := m.sessionState(s.ID)
		line := cursor + stateIcon[state] + " " + s.Name
		if _, running, _ := strings.Cut(m.status[s.ID], " "); state == "background" && running != "" {
			line += stateStyle[state].Render("  " + running + " running")
		}
		if g := store.SessionGroup(m.sessions, m.groups, s.ID); g != nil {
			label := "  ◆ group"
			if others := slices.DeleteFunc(slices.Clone(g.Tickets), func(k string) bool { return k == m.listKey() }); len(others) > 0 {
				label = "  ◆ with " + strings.Join(others, " ")
			}
			line += lipgloss.NewStyle().Foreground(groupColor(*g)).Render(label)
		}
		lines = append(lines, ansi.Truncate(line+dim.Render(fmt.Sprintf("  %.10s", s.CreatedAt)), w, "…"))
	}
	if m.pending != "" {
		action := map[byte]string{'x': "stop", 'd': "remove"}[m.pending[0]]
		if _, live := m.live[m.pending[1:]]; live && m.pending[0] == 'd' {
			action = "stop and remove"
		}
		lines = append(lines, errStyle.Render(fmt.Sprintf("press %c again to %s", m.pending[0], action)))
	}
	return lines
}

var (
	priorities = []string{"", "urgent", "high", "medium", "low"}
	stateOrder = map[string]int{"triage": 0, "backlog": 1, "unstarted": 2, "started": 3, "completed": 4, "canceled": 5}
	stateRank  = map[string]int{"stopped": 0, "background": 1, "working": 2, "waiting": 3}

	accent     = lipgloss.Color("#5e6ad2")
	focusColor = lipgloss.Color("#7c86ff")
	openColor  = lipgloss.Color("#3fb950")
	dim        = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b919c"))
	bold       = lipgloss.NewStyle().Bold(true)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149"))
	cardStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#3a4050")).Padding(0, 1)
	prioStyle  = map[int]lipgloss.Style{1: errStyle, 2: lipgloss.NewStyle().Foreground(lipgloss.Color("#f0883e")), 3: dim, 4: dim}
	stateStyle = map[string]lipgloss.Style{
		"working":    lipgloss.NewStyle().Foreground(openColor),
		"waiting":    lipgloss.NewStyle().Foreground(lipgloss.Color("#d29922")),
		"background": lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")),
		"stopped":    dim,
	}
	prStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a7bb8"))
	markColor = lipgloss.Color("#d2a8ff")
	markStyle = lipgloss.NewStyle().Foreground(markColor)

	stateGlyph = map[string]string{"working": "●", "waiting": "◉", "background": "◐", "stopped": "○"}
	stateIcon  = map[string]string{
		"working":    stateStyle["working"].Render(stateGlyph["working"]),
		"waiting":    stateStyle["waiting"].Render(stateGlyph["waiting"]),
		"background": stateStyle["background"].Render(stateGlyph["background"]),
		"stopped":    stateStyle["stopped"].Render(stateGlyph["stopped"]),
	}
)
