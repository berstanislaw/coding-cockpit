package ui

import (
	"cmp"
	"fmt"
	"kta/internal/claude"
	"kta/internal/config"
	"kta/internal/github"
	"kta/internal/shell"
	"kta/internal/store"
	"kta/internal/tmux"
	"kta/internal/tracker"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) titleOf(id string) string { return store.SessionTitle(m.sessions, m.groups, id) }

func (m model) groupOf(ticket string) *store.Group {
	return store.GroupOf(m.groups, ticket)
}

func (m model) sessionWork(id string) (string, []string) {
	if g := store.SessionGroup(m.sessions, m.groups, id); g != nil {
		return g.ID, g.Tickets
	}
	var out []string
	for _, k := range store.KeysOf(m.sessions, id) {
		if store.IsTicketKey(k) {
			out = append(out, k)
		}
	}
	return "", out
}

func (m model) ktaArgs(groupID string, tickets []string) []string {
	if len(tickets) == 0 {
		return nil
	}
	kta := shell.Quote(config.SelfExe)
	work := "This session works on " + strings.Join(tickets, ", ")
	if groupID != "" {
		work += " (group " + groupID + ")"
	}
	lines := []string{
		"You're running inside kta, a terminal kanban of the user's " + m.tracker.Name + " tickets where each ticket has its own Claude Code sessions. " + work + ".",
		m.tracker.APIHint,
	}
	var prs []string
	for _, t := range tickets {
		if p := m.prsFor(t); len(p) > 0 {
			prs = append(prs, t+": "+strings.Join(p, ", "))
		}
	}
	if len(prs) > 0 {
		lines = append(lines, "Pull requests linked to them in "+m.tracker.Name+": "+strings.Join(prs, "; ")+".")
	}
	lines = append(lines,
		"Tickets can be grouped so one session works on several of them, and a ticket belongs to one group at most. When the user asks to add tickets to this work, bring them over from another group, take some out, or asks what is grouped, use these commands; the kanban changes right away:",
		"- `"+kta+" group` lists this session's tickets (the list above is from when the session opened)",
		"- `"+kta+" group add KAN-123 …` adds tickets, moving them out of any other group, and prints each one in full: read them, they are part of this work now",
		"- `"+kta+" group remove KAN-123 …` takes tickets out",
		"- `"+kta+" groups` lists every group with its id; add `--group <id>` to change a group other than this one",
	)
	return []string{"--append-system-prompt", strings.Join(lines, "\n")}
}

func (m model) sessionKeys(key string) []string {
	if g := m.groupOf(key); g != nil {
		return []string{key, store.GroupKey(g.ID)}
	}
	return []string{key}
}

func (m model) sessionsOf(key string) []store.Session {
	var out []store.Session
	for _, k := range m.sessionKeys(key) {
		out = append(out, m.sessions[k]...)
	}
	return out
}

func (m model) foldKey(key string) (model, tea.Cmd) {
	var target *store.Group
	if i := m.selectedIssue(); i != nil {
		target = m.groupOf(i.Key)
	}
	if key == "z" && target == nil {
		return m, nil
	}
	folded := slices.ContainsFunc(m.groups, func(g store.Group) bool { return g.Collapsed })
	groups, err := store.MutateGroups(func(gs []store.Group) []store.Group {
		for k := range gs {
			switch {
			case key == "Z":
				gs[k].Collapsed = !folded
			case gs[k].ID == target.ID:
				gs[k].Collapsed = !gs[k].Collapsed
			}
		}
		return gs
	})
	if err != nil {
		m.err = err
		return m, nil
	}
	m.groups = groups
	m.buildColumns()
	return m, nil
}

func (m *model) collapse(items []tracker.Issue) []tracker.Issue {
	var kept []tracker.Issue
	hidden := map[string]bool{}
	for _, i := range items {
		if hidden[i.Key] {
			continue
		}
		kept = append(kept, i)
		g := m.groupOf(i.Key)
		if g == nil || !g.Collapsed {
			continue
		}
		cg := cardGroup{Group: *g, issues: []tracker.Issue{i}}
		for _, j := range items {
			if j.Key != i.Key && !hidden[j.Key] && slices.Contains(g.Tickets, j.Key) {
				hidden[j.Key] = true
				cg.issues = append(cg.issues, j)
			}
		}
		if len(cg.issues) > 1 {
			m.collapsed[i.Key] = cg
		}
	}
	return kept
}

func (m model) clusterGroups(items []tracker.Issue) []tracker.Issue {
	out := make([]tracker.Issue, 0, len(items))
	placed := map[string]bool{}
	for _, i := range items {
		if placed[i.Key] {
			continue
		}
		out, placed[i.Key] = append(out, i), true
		if g := m.groupOf(i.Key); g != nil {
			for _, j := range items {
				if !placed[j.Key] && slices.Contains(g.Tickets, j.Key) {
					out, placed[j.Key] = append(out, j), true
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
	if g := store.GroupByID(m.groups, m.newIn); g != nil {
		return g.Tickets
	}
	if len(m.marked) < 2 {
		return nil
	}
	return slices.Sorted(slices.Values(m.marked))
}

func (m *model) cleanupGroups() {
	if ids, drop := store.StaleGroups(m.sessions, m.groups, m.live); len(ids) == 0 && len(drop) == 0 {
		return
	}
	var dropped []string
	sessions, groups, err := store.MutateState(func(s map[string][]store.Session, gs []store.Group) []store.Group {
		ids, drop := store.StaleGroups(s, gs, m.live)
		dropped = ids
		for k := range s {
			if s[k] = slices.DeleteFunc(s[k], func(x store.Session) bool { return slices.Contains(ids, x.ID) }); len(s[k]) == 0 {
				delete(s, k)
			}
		}
		return slices.DeleteFunc(gs, func(g store.Group) bool { return slices.Contains(drop, g.ID) })
	})
	if err != nil {
		m.err = err
		return
	}
	for _, id := range dropped {
		os.Remove(filepath.Join(config.StatusDir, id))
	}
	m.sessions, m.groups = sessions, groups
}

func (m model) openTicket(withNew bool) (model, tea.Cmd) {
	i := m.selectedIssue()
	if i == nil {
		return m, nil
	}
	var cmds []tea.Cmd
	if m.ticket == nil || m.ticket.Key != i.Key {
		issue := *i
		m.ticket, m.sel, m.mdRaw, m.mdWidth = &issue, 0, "", 0
		m.vp.GotoTop()
		cmds = append(cmds, m.loadDetailCmd(issue.Key))
	}
	var err error
	if pane := m.bestPane(i.Key); pane != "" {
		err = tmux.ShowRight(pane)
	} else {
		err = tmux.HideRight()
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
	if err := tmux.HideRight(); err != nil {
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
	if m.ticket != nil && m.ticket.Key == key {
		fromTicket = issuePRs(*m.ticket)
	}
	var out []string
	for _, u := range slices.Concat(fromTicket, m.ticketPRs[key]) {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

func (m model) cardKeys(i tracker.Issue) []string {
	if cg := m.collapsed[i.Key]; len(cg.issues) > 1 {
		return cg.Tickets
	}
	return []string{i.Key}
}

func (m *model) openPRs(keys ...string) {
	var prs []string
	for _, key := range keys {
		prs = append(prs, m.prsFor(key)...)
	}
	if len(prs) == 0 {
		m.err = fmt.Errorf("%s has no PRs in %s", strings.Join(keys, ", "), m.tracker.Name)
		return
	}
	for _, url := range prs {
		shell.OpenURL(url)
	}
}

func (m model) submit() (model, tea.Cmd) {
	key, request := m.listKey(), m.ta.Value()
	list := m.sessionsOf(key)
	rec := store.Session{ID: store.NewUUID(), Name: claude.SessionName(request, len(list)+1), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	title := store.TitleFor(key, rec.Name)
	m.focus = focusStrip
	if keys := m.groupKeys(); keys != nil {
		var list []store.Session
		for _, k := range keys {
			for _, s := range m.sessionsOf(k) {
				if !slices.ContainsFunc(list, func(x store.Session) bool { return x.ID == s.ID }) {
					list = append(list, s)
				}
			}
		}
		previous := claude.PreviousSessions(list, m.live)
		rec.Name = claude.SessionName(request, len(list)+1)
		var fresh *store.Group
		target := store.GroupKey(m.newIn)
		if store.GroupByID(m.groups, m.newIn) == nil {
			fresh = &store.Group{ID: store.NewUUID(), Tickets: keys, Hue: store.PickHue(store.UsedHues(m.groups)), CreatedAt: rec.CreatedAt}
			target = store.GroupKey(fresh.ID)
			m.marked = nil
		}
		title := store.TitleFor(strings.Join(keys, "+"), rec.Name)
		extra := m.ktaArgs(strings.TrimPrefix(target, store.GroupPrefix), keys)
		m.newIn = ""
		return m, func() tea.Msg {
			issues := make([]tracker.Issue, len(keys))
			for i, k := range keys {
				issue, err := m.tracker.Issue(k)
				if err != nil {
					return launchedMsg{err: err}
				}
				issues[i] = issue
			}
			pane, err := claude.Launch(rec.ID, title, append(append([]string{"--session-id", rec.ID, "-n", title}, extra...), "--", claude.BuildPrompt(m.tracker.Kind, issues, request, previous))...)
			return launchedMsg{keys: []string{target}, group: fresh, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	if key == store.FreeKey {
		return m, func() tea.Msg {
			args := []string{"--session-id", rec.ID, "-n", title}
			if strings.TrimSpace(request) != "" {
				args = append(args, "--", request)
			}
			pane, err := claude.Launch(rec.ID, title, args...)
			return launchedMsg{keys: []string{key}, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	previous := claude.PreviousSessions(list, m.live)
	if github.IsPRKey(key) && m.openPR != nil {
		p := *m.openPR
		return m, func() tea.Msg {
			pane, err := claude.Launch(rec.ID, title, "--session-id", rec.ID, "-n", title, "--", claude.PRPrompt(p, request, previous))
			return launchedMsg{keys: []string{key}, id: rec.ID, pane: pane, rec: &rec, err: err}
		}
	}
	extra := m.ktaArgs("", []string{key})
	return m, func() tea.Msg {
		issue, err := m.tracker.Issue(key)
		if err != nil {
			return launchedMsg{err: err}
		}
		pane, err := claude.Launch(rec.ID, title, append(append([]string{"--session-id", rec.ID, "-n", title}, extra...), "--", claude.BuildPrompt(m.tracker.Kind, []tracker.Issue{issue}, request, previous))...)
		return launchedMsg{keys: []string{key}, id: rec.ID, pane: pane, rec: &rec, err: err}
	}
}

func (m *model) removeSession(key, id string) {
	sessions, err := store.MutateSessions(func(s map[string][]store.Session) {
		for k := range s {
			if s[k] = slices.DeleteFunc(s[k], func(x store.Session) bool { return x.ID == id }); len(s[k]) == 0 {
				delete(s, k)
			}
		}
	})
	if err != nil {
		m.err = err
		return
	}
	os.Remove(filepath.Join(config.StatusDir, id))
	m.sessions = sessions
	m.cleanupGroups()
	m.sel = max(0, min(m.sel, len(m.prsFor(key))+len(m.sessionsOf(key))-1))
}

func (m model) leftovers(key string) bool {
	stopped := slices.ContainsFunc(m.sessions[key], func(s store.Session) bool {
		_, live := m.live[s.ID]
		return !live
	})
	return stopped || m.groupOf(key) != nil
}

func (m *model) forget(keys []string) {
	var ids []string
	sessions, groups, err := store.MutateState(func(s map[string][]store.Session, gs []store.Group) []store.Group {
		ids = nil
		for _, key := range keys {
			s[key] = slices.DeleteFunc(s[key], func(x store.Session) bool {
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
		return store.DropTickets(gs, keys)
	})
	if err != nil {
		m.err = err
		return
	}
	for _, id := range ids {
		if len(store.KeysOf(sessions, id)) == 0 {
			os.Remove(filepath.Join(config.StatusDir, id))
		}
	}
	m.sessions, m.groups = sessions, groups
	m.cleanupGroups()
	m.sel = max(0, min(m.sel, len(m.prsFor(m.listKey()))+len(m.sessionsOf(m.listKey()))-1))
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
		return store.FreeKey
	case m.tab == tabReviews:
		if m.openPR != nil && m.openPR.Mine {
			return github.PRKey(*m.openPR)
		}
		return ""
	case m.ticket != nil:
		return m.ticket.Key
	}
	return ""
}

func (m model) listSessions() []store.Session {
	out := slices.Clone(m.sessionsOf(m.listKey()))
	slices.SortStableFunc(out, func(a, b store.Session) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })
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
	for _, s := range m.sessionsOf(key) {
		if p, ok := m.live[s.ID]; ok && stateRank[m.sessionState(s.ID)] > best {
			best, pane = stateRank[m.sessionState(s.ID)], p
		}
	}
	return pane
}

func (m model) sessionRank(key string) int {
	rank := 2
	for _, s := range m.sessionsOf(key) {
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
		selected = i.Key
	}
	q := strings.ToLower(m.search.Value())
	index := map[string]int{}
	m.cols, m.collapsed = nil, map[string]cardGroup{}
	for _, st := range m.states {
		if _, known := stateOrder[st.Type]; known && !slices.Contains(m.settings.HiddenColumns, st.Name) {
			if _, ok := index[st.Name]; !ok {
				index[st.Name] = len(m.cols)
				m.cols = append(m.cols, column{state: st})
			}
		}
	}
	for _, i := range m.issues {
		if q != "" && !strings.Contains(strings.ToLower(i.Key+" "+i.Title), q) {
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
	weight := func(p int) int {
		if p == 0 {
			return 5
		}
		return p
	}
	for _, c := range m.cols {
		sort.SliceStable(c.items, func(a, b int) bool {
			ia, ib := c.items[a], c.items[b]
			if ra, rb := m.sessionRank(ia.Key), m.sessionRank(ib.Key); ra != rb {
				return ra < rb
			}
			if weight(ia.Priority) != weight(ib.Priority) {
				return weight(ia.Priority) < weight(ib.Priority)
			}
			return ia.UpdatedAt.After(ib.UpdatedAt)
		})
	}
	for c := range m.cols {
		m.cols[c].items = m.collapse(m.clusterGroups(m.cols[c].items))
	}
	for c := range m.cols {
		for r, i := range m.cols[c].items {
			if i.Key == selected || slices.ContainsFunc(m.collapsed[i.Key].issues, func(x tracker.Issue) bool { return x.Key == selected }) {
				m.col, m.row = c, r
			}
		}
	}
	m.clamp()
}

func stateCmp(a, b tracker.State) int {
	order := func(t string) int {
		if o, ok := stateOrder[t]; ok {
			return o
		}
		return 9
	}
	return cmp.Or(cmp.Compare(order(a.Type), order(b.Type)), cmp.Compare(a.Position, b.Position), cmp.Compare(a.Name, b.Name))
}

func (m *model) clamp() {
	m.col = max(0, min(m.col, len(m.cols)-1))
	if len(m.cols) == 0 {
		m.row = 0
		return
	}
	m.row = max(0, min(m.row, len(m.cols[m.col].items)-1))
}

func (m model) selectedIssue() *tracker.Issue {
	if len(m.cols) == 0 || len(m.cols[m.col].items) == 0 {
		return nil
	}
	return &m.cols[m.col].items[m.row]
}

func issuePRs(i tracker.Issue) []string {
	var out []string
	for _, u := range i.Links {
		if github.URLPattern.MatchString(u) {
			out = append(out, github.CanonicalPR(u))
		}
	}
	return out
}

type column struct {
	state tracker.State
	items []tracker.Issue
}

type cardGroup struct {
	store.Group
	issues []tracker.Issue
}
