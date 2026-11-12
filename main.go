package main

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"slices"
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
	"golang.org/x/term"
)

type focus int

const (
	focusBoard focus = iota
	focusStrip
	focusNew
	focusAsk
)

type tab int

const (
	tabKanban tab = iota
	tabSessions
	tabReviews
	tabDaily
	tabSettings
)

const freeKey = "free"

var tabNames = []string{"kanban", "sessions", "reviews", "daily", "settings"}

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
	prStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a7bb8"))
	markColor  = lipgloss.Color("#d2a8ff")
	markStyle  = lipgloss.NewStyle().Foreground(markColor)
	markHue    = 270
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
		keys     []string
		id, pane string
		rec      *Session
		err      error
	}
	doneMsg      []string
	tickMsg      struct{}
	animMsg      struct{}
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
	linearPRs   map[string][]string
	daily       string
	dailyAt     time.Time
	dailyBusy   bool
	dailyVP     viewport.Model
	dailyW      int
	settings    Settings
	states      []State
	setSel      int
	showKeys    bool
	prs         []PR
	prSel       int
	openPR      *PR
	reviews     map[string]*Review
	thread      int
	ask         textinput.Model
	askKind     string
	busy        map[string]bool
	generating  map[string]bool
	sending     map[string]bool
	labelPool   []string
	prsLoading  bool
	rvp         viewport.Model
	rvpKey      string
	marked      []string
	newGroup    []string
	collapsed   map[string]cardGroup
	frame       int
	animOn      bool
}

func loadBoardCmd(sources []string) tea.Cmd {
	return func() tea.Msg { issues, err := fetchBoard(sources); return boardMsg{issues, err} }
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

func doneCmd(keys []string) tea.Cmd {
	return func() tea.Msg {
		done, err := fetchDone(keys)
		if err != nil || len(done) == 0 {
			return nil
		}
		return doneMsg(done)
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
	keys := keysOf(m.sessions, id)
	if len(keys) == 0 {
		return ""
	}
	for _, s := range m.sessions[keys[0]] {
		if s.ID == id {
			return titleFor(strings.Join(keys, "+"), s.Name)
		}
	}
	return ""
}

type cardGroup struct {
	id      string
	members []string
	color   lipgloss.Color
	issues  []Issue
}

func (g cardGroup) key() string {
	return g.id
}

func (m model) allGroups() []string {
	var keys []string
	for key := range m.sessions {
		for _, g := range m.groupsOf(key) {
			if !slices.Contains(keys, g.key()) {
				keys = append(keys, g.key())
			}
		}
	}
	return keys
}

func (m model) foldKey(key string) (model, tea.Cmd) {
	i := m.selectedIssue()
	next := m.settings
	switch {
	case key == "Z" && len(next.CollapsedGroups) > 0:
		next.CollapsedGroups = nil
	case key == "Z":
		next.CollapsedGroups = m.allGroups()
	case i == nil:
		return m, nil
	case m.collapsed[i.Identifier].members != nil:
		next.CollapsedGroups = slices.DeleteFunc(slices.Clone(next.CollapsedGroups), func(k string) bool { return k == m.collapsed[i.Identifier].key() })
	default:
		gs := m.groupsOf(i.Identifier)
		if len(gs) == 0 {
			return m, nil
		}
		next.CollapsedGroups = toggle(next.CollapsedGroups, gs[0].key())
	}
	return m.applySettings(next)
}

func (m *model) collapse(items []Issue) []Issue {
	var kept []Issue
	hidden := map[string]bool{}
	for _, i := range items {
		if hidden[i.Identifier] {
			continue
		}
		kept = append(kept, i)
		for _, g := range m.groupsOf(i.Identifier) {
			if !slices.Contains(m.settings.CollapsedGroups, g.key()) {
				continue
			}
			g.issues = []Issue{i}
			for _, j := range items {
				if j.Identifier != i.Identifier && !hidden[j.Identifier] && slices.Contains(g.members, j.Identifier) {
					hidden[j.Identifier] = true
					g.issues = append(g.issues, j)
				}
			}
			if len(g.issues) > 1 {
				m.collapsed[i.Identifier] = g
				break
			}
		}
	}
	return kept
}

func (m model) groupsOf(key string) []cardGroup {
	var groups []cardGroup
	for _, s := range m.sessions[key] {
		keys := keysOf(m.sessions, s.ID)
		if len(keys) < 2 || slices.ContainsFunc(groups, func(g cardGroup) bool { return slices.Equal(g.members, keys) }) {
			continue
		}
		hue := 0
		if s.Hue != nil {
			hue = *s.Hue
		} else {
			h := fnv.New32a()
			h.Write([]byte(strings.Join(keys, "+")))
			hue = int(h.Sum32() % 360)
		}
		groups = append(groups, cardGroup{id: s.ID, members: keys, color: hueColor(float64(hue))})
	}
	return groups
}

func (m model) usedHues() []int {
	hues := []int{markHue}
	for _, list := range m.sessions {
		for _, s := range list {
			if s.Hue != nil && !slices.Contains(hues, *s.Hue) {
				hues = append(hues, *s.Hue)
			}
		}
	}
	return hues
}

func pickHue(used []int) int {
	best, bestDist := []int{}, -1
	for h := 0; h < 360; h += 15 {
		dist := 360
		for _, u := range used {
			d := (h - u + 360) % 360
			dist = min(dist, d, 360-d)
		}
		switch {
		case dist > bestDist:
			best, bestDist = []int{h}, dist
		case dist == bestDist:
			best = append(best, h)
		}
	}
	return best[rand.IntN(len(best))]
}

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

func (m model) clusterGroups(items []Issue) []Issue {
	out := make([]Issue, 0, len(items))
	placed := map[string]bool{}
	for _, i := range items {
		if placed[i.Identifier] {
			continue
		}
		out, placed[i.Identifier] = append(out, i), true
		for _, g := range m.groupsOf(i.Identifier) {
			for _, j := range items {
				if !placed[j.Identifier] && slices.Contains(g.members, j.Identifier) {
					out, placed[j.Identifier] = append(out, j), true
				}
			}
		}
	}
	return out
}

func (m model) groupKeys() []string {
	if m.tab != tabKanban {
		return nil
	}
	if len(m.newGroup) > 1 {
		return m.newGroup
	}
	if len(m.marked) < 2 {
		return nil
	}
	return slices.Sorted(slices.Values(m.marked))
}

func openURL(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	exec.Command(opener, url).Start()
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadBoardCmd(m.settings.sources()), refreshStateCmd(), tick(), boardTick())
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
		cmds := []tea.Cmd{loadBoardCmd(m.settings.sources()), boardTick()}
		if m.tab == tabReviews {
			cmds = append(cmds, prsCmd(m.settings.ReviewLabels))
		}
		return m, tea.Batch(cmds...)
	case boardMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.issues = msg.issues
			m.linearPRs = map[string][]string{}
			for _, i := range msg.issues {
				if prs := issuePRs(i); len(prs) > 0 {
					m.linearPRs[i.Identifier] = prs
				}
			}
			m.buildColumns()
			for _, i := range msg.issues {
				if m.ticket != nil && i.Identifier == m.ticket.Identifier {
					m.ticket.State, m.ticket.Title, m.ticket.Priority = i.State, i.Title, i.Priority
				}
			}
			var gone []string
			for key := range m.sessions {
				if key != freeKey && !isPRKey(key) && !slices.ContainsFunc(msg.issues, func(i Issue) bool { return i.Identifier == key }) {
					gone = append(gone, key)
				}
			}
			if len(gone) > 0 {
				cmd = doneCmd(gone)
			}
		}
	case prsMsg, reviewMsg, askMsg, sentMsg:
		m = m.updateReviews(msg)
	case doneMsg:
		m.forget(msg)
	case statesMsg:
		m.states = msg
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
	case animMsg:
		m.frame++
		if m.animating() {
			return m, animTick()
		}
		m.animOn = false
		return m, nil
	case tickMsg:
		return m, tea.Batch(refreshStateCmd(), tick())
	case launchedMsg:
		if msg.err != nil {
			m.err = msg.err
			break
		}
		if msg.rec != nil {
			sessions, err := mutateSessions(func(s map[string][]Session) {
				for _, key := range msg.keys {
					s[key] = append(s[key], *msg.rec)
				}
			})
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
	case m.tab == tabReviews:
		m.fitReviews()
	default:
		m.ta.SetWidth(m.inner())
		m.ta.SetHeight(5)
	}
	if !m.animOn && m.animating() {
		m.animOn = true
		cmd = tea.Batch(cmd, animTick())
	}
	return m, cmd
}

func animTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

func (m model) animating() bool {
	return m.tab == tabKanban && len(m.collapsed) > 0
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
	typing := m.focus == focusNew || m.focus == focusAsk || m.searching
	switch key {
	case "ctrl+c":
		return m, tea.Quit
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
		case m.tab == tabReviews:
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
			m.focus, m.newGroup = focusStrip, nil
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
		m.newGroup = nil
		if i := m.selectedIssue(); i != nil && len(m.collapsed[i.Identifier].members) > 1 {
			m.newGroup = m.collapsed[i.Identifier].members
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
			return m, resumeCmd(s.ID, m.titleOf(s.ID))
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

func (m model) prsFor(key string) []string {
	var fromTicket []string
	if m.ticket != nil && m.ticket.Identifier == key {
		fromTicket = issuePRs(*m.ticket)
	}
	var out []string
	for _, u := range slices.Concat(fromTicket, m.linearPRs[key]) {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

func (m model) cardKeys(i Issue) []string {
	if cg := m.collapsed[i.Identifier]; len(cg.issues) > 1 {
		return cg.members
	}
	return []string{i.Identifier}
}

func (m *model) openPRs(keys ...string) {
	var prs []string
	for _, key := range keys {
		prs = append(prs, m.prsFor(key)...)
	}
	if len(prs) == 0 {
		m.err = fmt.Errorf("%s has no PRs in Linear", strings.Join(keys, ", "))
		return
	}
	for _, url := range prs {
		openURL(url)
	}
}

func (m model) submit() (model, tea.Cmd) {
	key, request := m.listKey(), m.ta.Value()
	list := m.sessions[key]
	rec := Session{ID: newUUID(), Name: sessionName(request, len(list)+1), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	title := titleFor(key, rec.Name)
	m.focus = focusStrip
	if keys := m.groupKeys(); keys != nil {
		var list []Session
		for _, k := range keys {
			for _, s := range m.sessions[k] {
				if !slices.ContainsFunc(list, func(x Session) bool { return x.ID == s.ID }) {
					list = append(list, s)
				}
			}
		}
		previous := previousSessions(list, m.live)
		rec.Name = sessionName(request, len(list)+1)
		hue := pickHue(m.usedHues())
		rec.Hue = &hue
		title := titleFor(strings.Join(keys, "+"), rec.Name)
		if m.newGroup == nil {
			m.marked = nil
		}
		m.newGroup = nil
		return m, func() tea.Msg {
			issues := make([]Issue, len(keys))
			for i, k := range keys {
				issue, err := fetchIssue(k)
				if err != nil {
					return launchedMsg{err: err}
				}
				issues[i] = issue
			}
			pane, err := launch(rec.ID, title, "--session-id", rec.ID, "-n", title, buildPrompt(issues, request, previous))
			return launchedMsg{keys: keys, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	if key == freeKey {
		return m, func() tea.Msg {
			args := []string{"--session-id", rec.ID, "-n", title}
			if strings.TrimSpace(request) != "" {
				args = append(args, request)
			}
			pane, err := launch(rec.ID, title, args...)
			return launchedMsg{keys: []string{key}, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	previous := previousSessions(list, m.live)
	if isPRKey(key) && m.openPR != nil {
		p := *m.openPR
		return m, func() tea.Msg {
			pane, err := launch(rec.ID, title, "--session-id", rec.ID, "-n", title, prPrompt(p, request, previous))
			return launchedMsg{keys: []string{key}, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	return m, func() tea.Msg {
		issue, err := fetchIssue(key)
		if err != nil {
			return launchedMsg{err: err}
		}
		pane, err := launch(rec.ID, title, "--session-id", rec.ID, "-n", title, buildPrompt([]Issue{issue}, request, previous))
		return launchedMsg{keys: []string{key}, id: rec.ID, pane: pane, rec: &rec, err: err}
	}
}

func (m *model) removeSession(key, id string) {
	sessions, err := mutateSessions(func(s map[string][]Session) {
		for k := range s {
			if s[k] = slices.DeleteFunc(s[k], func(x Session) bool { return x.ID == id }); len(s[k]) == 0 {
				delete(s, k)
			}
		}
	})
	if err != nil {
		m.err = err
		return
	}
	os.Remove(statusDir + "/" + id)
	m.sessions = sessions
	m.sel = max(0, min(m.sel, len(m.prsFor(key))+len(sessions[key])-1))
}

func (m *model) forget(keys []string) {
	var ids []string
	sessions, err := mutateSessions(func(s map[string][]Session) {
		for _, key := range keys {
			s[key] = slices.DeleteFunc(s[key], func(x Session) bool {
				_, live := m.live[x.ID]
				if !live {
					ids = append(ids, x.ID)
				}
				return !live
			})
			if len(s[key]) == 0 {
				delete(s, key)
			}
		}
	})
	if err != nil {
		m.err = err
		return
	}
	for _, id := range ids {
		if len(keysOf(sessions, id)) == 0 {
			os.Remove(statusDir + "/" + id)
		}
	}
	m.sessions = sessions
	m.sel = max(0, min(m.sel, len(m.prsFor(m.listKey()))+len(sessions[m.listKey()])-1))
}

func (m *model) selectSession(id string) {
	for i, s := range m.listSessions() {
		if s.ID == id {
			m.sel = len(m.prsFor(m.listKey())) + i
		}
	}
}

func (m model) listKey() string {
	switch {
	case m.tab == tabSessions:
		return freeKey
	case m.tab == tabReviews:
		if m.openPR != nil && m.openPR.Mine {
			return prKey(*m.openPR)
		}
		return ""
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
	m.cols, m.collapsed = nil, map[string]cardGroup{}
	for _, i := range m.issues {
		if q != "" && !strings.Contains(strings.ToLower(i.Identifier+" "+i.Title), q) {
			continue
		}
		if slices.Contains(m.settings.HiddenColumns, i.State.Name) {
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
	slices.SortStableFunc(m.cols, func(a, b column) int { return stateCmp(a.state, b.state) })
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
		m.cols[c].items = m.collapse(m.clusterGroups(m.cols[c].items))
	}
	for c := range m.cols {
		for r, i := range m.cols[c].items {
			if i.Identifier == selected || slices.ContainsFunc(m.collapsed[i.Identifier].issues, func(x Issue) bool { return x.Identifier == selected }) {
				m.col, m.row = c, r
			}
		}
	}
	m.clamp()
}

func stateCmp(a, b State) int {
	order := func(t string) int {
		if o, ok := stateOrder[t]; ok {
			return o
		}
		return 9
	}
	return cmp.Or(cmp.Compare(order(a.Type), order(b.Type)), cmp.Compare(a.Position, b.Position))
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
	var body string
	switch {
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
	return append(keys, tabs, quit)
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
	if m.focus != focusNew && m.focus != focusAsk && !m.searching {
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
	for i, name := range tabNames {
		if m.settings.tabHidden(tab(i)) {
			continue
		}
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
	cardH := func(i Issue) int { return lipgloss.Height(m.card(i, cw-1, false)) }
	render := func(i Issue, cursor bool) string {
		c := m.card(i, cw-1, cursor)
		bar := " "
		if cg := m.collapsed[i.Identifier]; cg.members != nil {
			bar = lipgloss.NewStyle().Foreground(cg.color).Render("┃")
		} else if gs := m.groupsOf(i.Identifier); len(gs) > 0 {
			bar = lipgloss.NewStyle().Foreground(gs[0].color).Render("┃")
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

func (m model) card(i Issue, cw int, cursor bool) string {
	inner := cw - 4
	marked := slices.Contains(m.marked, i.Identifier)
	meta := i.Identifier
	if marked {
		meta = markStyle.Render("✓ " + i.Identifier)
	}
	if p := int(i.Priority); p > 0 && p < len(priorities) {
		meta += " " + prioStyle[p].Render(priorities[p])
	}
	if b := m.badge(i.Identifier); b != "" {
		meta += " " + b
	}
	for _, g := range m.groupsOf(i.Identifier) {
		meta += " " + lipgloss.NewStyle().Foreground(g.color).Render("◆")
	}
	var mate lipgloss.Color
	if sel := m.selectedIssue(); sel != nil && sel.Identifier != i.Identifier {
		for _, g := range m.groupsOf(sel.Identifier) {
			if slices.Contains(g.members, i.Identifier) {
				mate = g.color
			}
		}
	}
	body := clampLines(i.Title, inner, 2)
	if prs := m.prsFor(i.Identifier); len(prs) > 0 {
		tags := make([]string, len(prs))
		for k, url := range prs {
			tags[k] = prStyle.Render(prTag(url))
		}
		titleLines := min(2, len(strings.Split(ansi.Wordwrap(i.Title, inner, ""), "\n")))
		body = clampLines(i.Title, inner, titleLines) + "\n" + ansi.Truncate(strings.Join(tags, " "), inner, "…")
	}
	if cg := m.collapsed[i.Identifier]; len(cg.issues) > 1 {
		ids := make([]string, len(cg.issues))
		for k, x := range cg.issues {
			ids[k] = x.Identifier
		}
		meta = lipgloss.NewStyle().Foreground(cg.color).Bold(true).Render(fmt.Sprintf("◆ %d cards", len(cg.issues)))
		if b := m.badge(ids...); b != "" {
			meta += " " + b
		}
		shown := cg.issues[m.frame/12%len(cg.issues)]
		body = marquee(strings.Join(ids, " · "), inner, m.frame) + "\n" + ansi.Truncate(lipgloss.NewStyle().Foreground(cg.color).Render(shown.Identifier)+" "+dim.Render(shown.Title), inner, "…")
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
		lines = append(lines, ansi.Truncate(cursorAt(idx)+prStyle.Render("PR")+" "+prLabel(url), w, "…"))
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
		if keys := keysOf(m.sessions, s.ID); len(keys) > 1 {
			others := slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return k == m.listKey() })
			color := lipgloss.Color("")
			for _, g := range m.groupsOf(m.listKey()) {
				if slices.Equal(g.members, keys) {
					color = g.color
				}
			}
			line += lipgloss.NewStyle().Foreground(color).Render("  ◆ with " + strings.Join(others, " "))
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
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	switch {
	case arg == "hook" && len(os.Args) == 4:
		runHook(os.Args[2], os.Args[3], os.Stdin)
		return
	case arg == "transcript" && len(os.Args) == 3:
		if err := printTranscript(os.Args[2], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	err := loadConfig()
	if arg == "setup" || (err != nil && arg == "check" && term.IsTerminal(int(os.Stdin.Fd()))) {
		err = setup()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if arg != "" {
		return
	}
	if selfPane == "" {
		fmt.Fprintln(os.Stderr, "run it through ./kta (needs tmux)")
		os.Exit(1)
	}
	tmux("set-environment", "-g", "LINEAR_API_KEY", apiKey)
	for _, d := range []string{statusDir, runDir, reviewsDir} {
		os.MkdirAll(d, 0o700)
	}
	sessions, err := loadSessions()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	settings, err := loadSettings()
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
	ask := textinput.New()
	ask.Prompt = ""
	m := model{sessions: sessions, settings: settings, live: map[string]string{}, status: map[string]string{}, search: search, ta: ta, vp: viewport.New(0, 0), loading: true, paneFocused: true,
		reviews: map[string]*Review{}, busy: map[string]bool{}, generating: map[string]bool{}, sending: map[string]bool{}, ask: ask, rvp: viewport.New(0, 0)}
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithReportFocus()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmux("kill-session")
}
