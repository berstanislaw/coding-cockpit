package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type focus int

const (
	focusBoard focus = iota
	focusStrip
	focusNew
)

type tab int

const (
	tabKanban tab = iota
	tabSessions
	tabReviews
	tabDaily
)

const freeKey = "free"

var tabNames = []string{"kanban", "sessions", "reviews", "daily"}

var (
	priorities = []string{"", "urgent", "high", "medium", "low"}
	stateOrder = map[string]int{"triage": 0, "backlog": 1, "unstarted": 2, "started": 3}
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
	stateGlyph = map[string]string{"working": "●", "waiting": "◉", "background": "◐", "stopped": "○"}
	stateIcon  = map[string]string{
		"working":    stateStyle["working"].Render(stateGlyph["working"]),
		"waiting":    stateStyle["waiting"].Render(stateGlyph["waiting"]),
		"background": stateStyle["background"].Render(stateGlyph["background"]),
		"stopped":    stateStyle["stopped"].Render(stateGlyph["stopped"]),
	}
)

type column struct {
	state State
	items []Issue
}

type (
	boardMsg struct {
		issues []Issue
		err    error
	}
	detailMsg struct {
		issue Issue
		err   error
	}
	stateMsg struct {
		live, status map[string]string
		untitled     []string
		sessions     map[string][]Session
		err          error
	}
	launchedMsg struct {
		key, id, pane string
		rec           *Session
		err           error
	}
	tickMsg      struct{}
	boardTickMsg struct{}
)

type model struct {
	w, h        int
	tab         tab
	focus       focus
	issues      []Issue
	cols        []column
	col, row    int
	search      textinput.Model
	searching   bool
	sessions    map[string][]Session
	live        map[string]string
	status      map[string]string
	ticket      *Issue
	sel         int
	vp          viewport.Model
	mdRaw       string
	mdWidth     int
	ta          textarea.Model
	pending     string
	loading     bool
	paneFocused bool
	err         error
	daily       string
	dailyAt     time.Time
	dailyBusy   bool
	dailyVP     viewport.Model
	dailyW      int
}

func loadBoardCmd() tea.Cmd {
	return func() tea.Msg { issues, err := fetchBoard(); return boardMsg{issues, err} }
}

func loadDetailCmd(key string) tea.Cmd {
	return func() tea.Msg { issue, err := fetchIssue(key); return detailMsg{issue, err} }
}

func refreshStateCmd() tea.Cmd {
	return func() tea.Msg {
		keepLeft()
		live, untitled, err := livePanes()
		if err != nil {
			return stateMsg{err: err}
		}
		sessions, err := loadSessions()
		return stateMsg{live, readStatus(), untitled, sessions, err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func boardTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return boardTickMsg{} })
}

func resumeCmd(id, title string) tea.Cmd {
	return func() tea.Msg {
		pane, err := launch(id, title, "--resume", id)
		return launchedMsg{id: id, pane: pane, err: err}
	}
}

func titleFor(key, name string) string {
	if key == freeKey {
		return name
	}
	return key + " · " + name
}

func (m model) titleOf(id string) string {
	for key, list := range m.sessions {
		for _, s := range list {
			if s.ID == id {
				return titleFor(key, s.Name)
			}
		}
	}
	return ""
}

func openURL(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	exec.Command(opener, url).Start()
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadBoardCmd(), refreshStateCmd(), tick(), boardTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.FocusMsg:
		m.paneFocused = true
	case tea.BlurMsg:
		m.paneFocused = false
	case boardTickMsg:
		return m, tea.Batch(loadBoardCmd(), boardTick())
	case boardMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.issues = msg.issues
			m.buildColumns()
			for _, i := range msg.issues {
				if m.ticket != nil && i.Identifier == m.ticket.Identifier {
					m.ticket.State, m.ticket.Title, m.ticket.Priority = i.State, i.Title, i.Priority
				}
			}
		}
	case detailMsg:
		if msg.err != nil {
			m.err = msg.err
			break
		}
		m.err = nil
		if m.ticket != nil && m.ticket.Identifier == msg.issue.Identifier {
			m.ticket = &msg.issue
			m.mdRaw, m.mdWidth = issueMarkdown(msg.issue), 0
		}
	case stateMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.live, m.status, m.sessions = msg.live, msg.status, msg.sessions
			m.buildColumns()
			for _, id := range msg.untitled {
				if title := m.titleOf(id); title != "" {
					setTitle(m.live[id], title)
				}
			}
		}
	case tickMsg:
		return m, tea.Batch(refreshStateCmd(), tick())
	case launchedMsg:
		if msg.err != nil {
			m.err = msg.err
			break
		}
		if msg.rec != nil {
			sessions, err := mutateSessions(func(s map[string][]Session) { s[msg.key] = append(s[msg.key], *msg.rec) })
			if err != nil {
				m.err = err
				break
			}
			m.sessions = sessions
		}
		m.live[msg.id] = msg.pane
		m.selectSession(msg.id)
		m.focus = focusStrip
		if m.tab == tabKanban {
			m.focus = focusBoard
		}
		if err := showRight(msg.pane); err != nil {
			m.err = err
		} else {
			focusPane(msg.pane)
		}
	case dailyMsg:
		m.dailyBusy = false
		if msg.err != nil {
			m.err = msg.err
			break
		}
		m.daily, m.dailyAt, m.dailyW = msg.text, msg.at, 0
	case tea.KeyMsg:
		m, cmd = m.handleKey(msg)
	}
	switch {
	case m.tab == tabKanban && m.ticket != nil:
		m.fitViewport()
	case m.tab == tabDaily:
		m.fitDaily()
	default:
		m.ta.SetWidth(m.inner())
		m.ta.SetHeight(5)
	}
	return m, cmd
}

func (m *model) fitDaily() {
	w := m.inner()
	m.dailyVP.Width, m.dailyVP.Height = w, max(1, m.h-5)
	if m.dailyW != w {
		m.dailyVP.SetContent(renderMarkdown(m.daily, w))
		m.dailyW = w
	}
}

func (m model) dailyKey(k tea.KeyMsg) (model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		if !m.dailyBusy {
			m.dailyBusy = true
			return m, dailyCmd(true)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.dailyVP, cmd = m.dailyVP.Update(k)
	return m, cmd
}

func (m model) dailyTop() string {
	since := dailySince(time.Now()).Format("Mon 02/01")
	switch {
	case m.dailyBusy:
		return dim.Render("since " + since + " · summarizing your Claude sessions…")
	case m.dailyAt.IsZero():
		return ""
	}
	return dim.Render("since " + since + " · generated " + m.dailyAt.Format("15:04"))
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
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "1", "2", "3", "4":
		if m.focus == focusNew || m.searching {
			break
		}
		fallthrough
	case "alt+1", "alt+2", "alt+3", "alt+4":
		m = m.switchTab(tab(key[len(key)-1] - '1'))
		if m.tab == tabDaily && !m.dailyBusy && m.dailyAt.Format(time.DateOnly) != time.Now().Format(time.DateOnly) {
			m.dailyBusy = true
			return m, dailyCmd(false)
		}
		return m, nil
	}
	if m.tab == tabDaily {
		return m.dailyKey(k)
	}
	if m.tab == tabReviews {
		if key == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	confirm := m.pending
	m.pending = ""
	switch {
	case m.focus == focusNew:
		switch key {
		case "esc":
			m.focus = focusStrip
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
		return m.openTicket(true)
	case "tab", "alt+down":
		if m.ticket != nil {
			m.focus = focusStrip
		}
	case "esc":
		m.closeTicket()
	case "/":
		m.searching = true
		return m, m.search.Focus()
	case "r":
		m.loading = true
		return m, loadBoardCmd()
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
	var s *Session
	if m.sel >= 0 && m.sel < len(list) {
		s = &list[m.sel]
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
		if m.tab == tabKanban {
			m.focus = focusBoard
		}
	case "up", "k":
		m.sel = max(0, m.sel-1)
	case "down", "j":
		m.sel = max(0, min(len(list)-1, m.sel+1))
	case "pgdown", "ctrl+d", " ":
		m.vp.HalfPageDown()
	case "pgup", "ctrl+u":
		m.vp.HalfPageUp()
	case "n":
		return m, m.startNew()
	case "o":
		if inTicket {
			openURL(m.ticket.URL)
		}
	case "r":
		if inTicket {
			return m, loadDetailCmd(m.ticket.Identifier)
		}
	case "enter":
		if s == nil {
			break
		}
		if !live {
			return m, resumeCmd(s.ID, titleFor(key0, s.Name))
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

func (m model) openTicket(withNew bool) (model, tea.Cmd) {
	i := m.selectedIssue()
	if i == nil {
		return m, nil
	}
	var cmds []tea.Cmd
	if m.ticket == nil || m.ticket.Identifier != i.Identifier {
		issue := *i
		m.ticket, m.sel, m.mdRaw, m.mdWidth = &issue, 0, "", 0
		m.vp.GotoTop()
		cmds = append(cmds, loadDetailCmd(issue.Identifier))
	}
	var err error
	if pane := m.bestPane(i.Identifier); pane != "" {
		err = showRight(pane)
	} else {
		err = hideRight()
	}
	if err != nil {
		m.err = err
	}
	if withNew {
		cmds = append(cmds, m.startNew())
	}
	return m, tea.Batch(cmds...)
}

func (m *model) closeTicket() {
	if err := hideRight(); err != nil {
		m.err = err
	}
	m.ticket, m.focus = nil, focusBoard
}

func (m *model) startNew() tea.Cmd {
	m.focus = focusNew
	m.ta.Reset()
	return m.ta.Focus()
}

func (m model) submit() (model, tea.Cmd) {
	key, request := m.listKey(), m.ta.Value()
	list := m.sessions[key]
	rec := Session{ID: newUUID(), Name: sessionName(request, len(list)+1), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	title := titleFor(key, rec.Name)
	m.focus = focusStrip
	if key == freeKey {
		return m, func() tea.Msg {
			args := []string{"--session-id", rec.ID, "-n", title}
			if strings.TrimSpace(request) != "" {
				args = append(args, request)
			}
			pane, err := launch(rec.ID, title, args...)
			return launchedMsg{key: key, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	previous := previousSessions(list, m.live)
	return m, func() tea.Msg {
		issue, err := fetchIssue(key)
		if err != nil {
			return launchedMsg{err: err}
		}
		pane, err := launch(rec.ID, title, "--session-id", rec.ID, "-n", title, buildPrompt(issue, request, previous))
		return launchedMsg{key: key, id: rec.ID, pane: pane, rec: &rec, err: err}
	}
}

func (m *model) removeSession(key, id string) {
	sessions, err := mutateSessions(func(s map[string][]Session) {
		var kept []Session
		for _, x := range s[key] {
			if x.ID != id {
				kept = append(kept, x)
			}
		}
		if len(kept) == 0 {
			delete(s, key)
		} else {
			s[key] = kept
		}
	})
	if err != nil {
		m.err = err
		return
	}
	os.Remove(statusDir + "/" + id)
	m.sessions = sessions
	m.sel = max(0, min(m.sel, len(sessions[key])-1))
}

func (m *model) selectSession(id string) {
	for i, s := range m.listSessions() {
		if s.ID == id {
			m.sel = i
		}
	}
}

func (m model) listKey() string {
	switch {
	case m.tab == tabSessions:
		return freeKey
	case m.ticket != nil:
		return m.ticket.Identifier
	}
	return ""
}

func (m model) listSessions() []Session {
	list := m.sessions[m.listKey()]
	out := make([]Session, len(list))
	for i, s := range list {
		out[len(list)-1-i] = s
	}
	return out
}

func (m model) sessionState(id string) string {
	if _, ok := m.live[id]; !ok {
		return "stopped"
	}
	switch state, _, _ := strings.Cut(m.status[id], " "); state {
	case "waiting", "background":
		return state
	}
	return "working"
}

func (m model) bestPane(key string) string {
	best, pane := -1, ""
	for _, s := range m.sessions[key] {
		if p, ok := m.live[s.ID]; ok && stateRank[m.sessionState(s.ID)] > best {
			best, pane = stateRank[m.sessionState(s.ID)], p
		}
	}
	return pane
}

func (m model) sessionRank(key string) int {
	rank := 2
	for _, s := range m.sessions[key] {
		if _, ok := m.live[s.ID]; ok {
			return 0
		}
		rank = 1
	}
	return rank
}

func (m *model) buildColumns() {
	selected := ""
	if i := m.selectedIssue(); i != nil {
		selected = i.Identifier
	}
	q := strings.ToLower(m.search.Value())
	index := map[string]int{}
	m.cols = nil
	for _, i := range m.issues {
		if q != "" && !strings.Contains(strings.ToLower(i.Identifier+" "+i.Title), q) {
			continue
		}
		k, ok := index[i.State.Name]
		if !ok {
			k = len(m.cols)
			index[i.State.Name] = k
			m.cols = append(m.cols, column{state: i.State})
		}
		m.cols[k].items = append(m.cols[k].items, i)
	}
	order := func(t string) int {
		if o, ok := stateOrder[t]; ok {
			return o
		}
		return 9
	}
	sort.SliceStable(m.cols, func(a, b int) bool {
		sa, sb := m.cols[a].state, m.cols[b].state
		if order(sa.Type) != order(sb.Type) {
			return order(sa.Type) < order(sb.Type)
		}
		return sa.Position < sb.Position
	})
	weight := func(p float64) float64 {
		if p == 0 {
			return 5
		}
		return p
	}
	for _, c := range m.cols {
		sort.SliceStable(c.items, func(a, b int) bool {
			ia, ib := c.items[a], c.items[b]
			if ra, rb := m.sessionRank(ia.Identifier), m.sessionRank(ib.Identifier); ra != rb {
				return ra < rb
			}
			if weight(ia.Priority) != weight(ib.Priority) {
				return weight(ia.Priority) < weight(ib.Priority)
			}
			return ia.UpdatedAt > ib.UpdatedAt
		})
	}
	for c := range m.cols {
		for r, i := range m.cols[c].items {
			if i.Identifier == selected {
				m.col, m.row = c, r
			}
		}
	}
	m.clamp()
}

func (m *model) clamp() {
	m.col = max(0, min(m.col, len(m.cols)-1))
	if len(m.cols) == 0 {
		m.row = 0
		return
	}
	m.row = max(0, min(m.row, len(m.cols[m.col].items)-1))
}

func (m model) selectedIssue() *Issue {
	if len(m.cols) == 0 || len(m.cols[m.col].items) == 0 {
		return nil
	}
	return &m.cols[m.col].items[m.row]
}

func issueMarkdown(i Issue) string {
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
	switch m.tab {
	case tabSessions:
		help := "↑↓ select · enter show · n new session · x stop · d remove · alt+→ claude · q quit"
		if m.focus == focusNew {
			help = "ctrl+s open session · esc cancel"
		}
		return strings.Join([]string{m.header(), m.box("sessions", true, m.h-4, strings.Join(m.sessionLines(), "\n")), dim.Render(ansi.Truncate(help, m.w, "…"))}, "\n")
	case tabReviews:
		return strings.Join([]string{m.header(), m.box("reviews", true, m.h-4, "\n  "+dim.Render("work in progress · nothing here yet")), dim.Render("alt+1 kanban · alt+2 sessions · q quit")}, "\n")
	case tabDaily:
		return strings.Join([]string{m.header(), m.box("daily", true, m.h-4, m.dailyTop()+"\n"+m.dailyVP.View()), dim.Render("↑↓ scroll · r regenerate · q quit")}, "\n")
	}
	help := "←→↑↓ move · enter open · o open in browser · n new session · / search · r refresh · q quit"
	stripH := 0
	if m.ticket != nil {
		stripH = m.stripHeight()
		help = "←→↑↓ move · enter open · o open in browser · n new session · alt+↓ ticket · esc close ticket · alt+→ claude"
		switch m.focus {
		case focusStrip:
			help = "↑↓ session · enter show · o open in browser · n new · x stop · d remove · pgup/pgdn scroll · alt+↑ board"
		case focusNew:
			help = "ctrl+s open session · esc cancel"
		}
	}
	boardH := m.h - 2 - stripH
	parts := []string{m.header(), m.box("kanban", m.focus == focusBoard, boardH-2, m.viewColumns(boardH-2))}
	if m.ticket != nil {
		parts = append(parts, m.box("ticket", m.focus != focusBoard, stripH-2, m.stripTop()+"\n"+m.vp.View()))
	}
	return strings.Join(append(parts, dim.Render(ansi.Truncate(help, m.w, "…"))), "\n")
}

func (m model) header() string {
	header := ""
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if tab(i) == m.tab {
			header += lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(accent).Render(label)
		} else {
			header += dim.Render(label)
		}
		if b := m.badge(freeKey); tab(i) == tabSessions && b != "" {
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
		if first, fit, _ := m.colWindow(); fit < len(m.cols) {
			header += dim.Render(fmt.Sprintf("  columns %d-%d of %d", first+1, first+fit, len(m.cols)))
		}
	}
	if m.loading {
		header += dim.Render("  loading…")
	}
	if m.err != nil {
		header += "  " + errStyle.Render(m.err.Error())
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
	visible := max(1, (height-1)/5)
	var blocks []string
	for c := first; c < min(n, first+fit); c++ {
		col := m.cols[c]
		head := lipgloss.NewStyle().Foreground(lipgloss.Color(col.state.Color)).Render("●") + " " + bold.Render(col.state.Name) + dim.Render(fmt.Sprintf(" %d", len(col.items)))
		lines := []string{ansi.Truncate(head, cw, "…")}
		offset := 0
		if c == m.col && m.row >= visible {
			offset = m.row - visible + 1
		}
		for r := offset; r < min(len(col.items), offset+visible); r++ {
			lines = append(lines, m.card(col.items[r], cw, c == m.col && r == m.row))
		}
		if c > first {
			blocks = append(blocks, " ")
		}
		blocks = append(blocks, lipgloss.NewStyle().Width(cw).Render(strings.Join(lines, "\n")))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

func (m model) card(i Issue, cw int, cursor bool) string {
	inner := cw - 4
	meta := i.Identifier
	if p := int(i.Priority); p > 0 && p < len(priorities) {
		meta += " " + prioStyle[p].Render(priorities[p])
	}
	if b := m.badge(i.Identifier); b != "" {
		meta += " " + b
	}
	style := cardStyle
	switch {
	case cursor && m.focus == focusBoard && m.paneFocused:
		style = style.Border(lipgloss.ThickBorder()).BorderForeground(focusColor)
	case m.ticket != nil && m.ticket.Identifier == i.Identifier:
		style = style.BorderForeground(openColor)
	case cursor:
		style = style.BorderForeground(lipgloss.Color("#8b919c"))
	}
	return style.Width(cw - 2).Render(ansi.Truncate(meta, inner, "…") + "\n" + clampLines(i.Title, inner, 2))
}

func (m model) badge(key string) string {
	count := map[string]int{}
	for _, s := range m.sessions[key] {
		count[m.sessionState(s.ID)]++
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
		return []string{bold.Render("New session"), m.ta.View()}
	}
	w := m.inner()
	list := m.listSessions()
	var lines []string
	if len(list) == 0 {
		lines = append(lines, dim.Render("no sessions · n to start one"))
	}
	for idx, s := range list {
		cursor := "  "
		if idx == m.sel && m.focus == focusStrip {
			cursor = lipgloss.NewStyle().Foreground(accent).Render("▸ ")
		}
		state := m.sessionState(s.ID)
		line := cursor + stateIcon[state] + " " + s.Name
		if _, running, _ := strings.Cut(m.status[s.ID], " "); state == "background" && running != "" {
			line += stateStyle[state].Render("  " + running + " running")
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

func main() {
	if err := loadConfig(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
	}
	if selfPane == "" {
		fmt.Fprintln(os.Stderr, "run it through ./board (needs tmux)")
		os.Exit(1)
	}
	tmux("set-environment", "-g", "LINEAR_API_KEY", apiKey)
	for _, d := range []string{statusDir, runDir} {
		os.MkdirAll(d, 0o700)
	}
	sessions, err := loadSessions()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "search"
	ta := textarea.New()
	ta.Placeholder = "what do you want in this session?"
	ta.ShowLineNumbers = false
	m := model{sessions: sessions, live: map[string]string{}, status: map[string]string{}, search: search, ta: ta, vp: viewport.New(0, 0), loading: true, paneFocused: true}
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithReportFocus()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmux("kill-session")
}
