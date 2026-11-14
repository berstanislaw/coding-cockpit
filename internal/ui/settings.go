package ui

import (
	"kta/internal/config"
	"kta/internal/github"
	"kta/internal/store"
	"kta/internal/tracker"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func tabHidden(s store.Settings, t tab) bool {
	return slices.Contains(hideableTabs, t) && slices.Contains(s.HiddenTabs, tabNames[t])
}

func toggle(list []string, v string) []string {
	if i := slices.Index(list, v); i >= 0 {
		return slices.Delete(slices.Clone(list), i, i+1)
	}
	return append(slices.Clone(list), v)
}

func teamKeys(issues []tracker.Issue) []string {
	var teams []string
	for _, i := range issues {
		if !slices.Contains(teams, i.Team) {
			teams = append(teams, i.Team)
		}
	}
	slices.Sort(teams)
	return teams
}

func (m model) statesCmd(issues []tracker.Issue) tea.Cmd {
	t, teams := m.tracker, teamKeys(issues)
	return func() tea.Msg {
		states, err := t.States(teams)
		if err != nil {
			return nil
		}
		return statesMsg(states)
	}
}

func (m model) columnNames() []string {
	var states []tracker.State
	for _, st := range m.states {
		if _, ok := stateOrder[st.Type]; ok {
			states = append(states, st)
		}
	}
	for _, i := range m.issues {
		states = append(states, i.State)
	}
	slices.SortStableFunc(states, stateCmp)
	var names []string
	for _, st := range append(states, m.hiddenStates()...) {
		if !slices.Contains(names, st.Name) {
			names = append(names, st.Name)
		}
	}
	return names
}

func (m model) hiddenStates() []tracker.State {
	out := make([]tracker.State, len(m.settings.HiddenColumns))
	for i, name := range m.settings.HiddenColumns {
		out[i] = tracker.State{Name: name}
	}
	return out
}

func (m model) settingItems() []settingItem {
	check := func(on bool, label string) string {
		if on {
			return lipgloss.NewStyle().Foreground(accent).Render("[x]") + " " + label
		}
		return dim.Render("[ ] " + label)
	}
	var items []settingItem
	for _, name := range m.columnNames() {
		items = append(items, settingItem{"Columns", check(!slices.Contains(m.settings.HiddenColumns, name), name), func(s *store.Settings, dir int) {
			if dir == 0 {
				s.HiddenColumns = toggle(s.HiddenColumns, name)
			}
		}})
	}
	for _, src := range tracker.Sources {
		if !m.tracker.Supports(src.Key) {
			continue
		}
		items = append(items, settingItem{"Tickets", check(slices.Contains(m.settings.Sources(), src.Key), src.Label), func(s *store.Settings, dir int) {
			if t := toggle(s.Sources(), src.Key); dir == 0 && len(t) > 0 {
				s.Tickets = t
			}
		}})
	}
	items = append(items, settingItem{"Daily", "language  ‹ " + m.settings.Language().Name + " ›", func(s *store.Settings, dir int) {
		if dir == 0 {
			dir = 1
		}
		i := slices.IndexFunc(store.Languages, func(l store.Language) bool { return l.Code == s.Language().Code })
		s.DailyLanguage = store.Languages[(i+dir+len(store.Languages))%len(store.Languages)].Code
	}})
	items = append(items, settingItem{"Notifications", check(!m.settings.NotificationsOff, "desktop notifications"), func(s *store.Settings, dir int) {
		if dir == 0 {
			s.NotificationsOff = !s.NotificationsOff
		}
	}})
	labels := slices.Clone(m.labelPool)
	for _, l := range m.settings.ReviewLabels {
		if !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	for _, l := range labels {
		items = append(items, settingItem{"Team labels for reviews", check(slices.Contains(m.settings.ReviewLabels, l), l), func(s *store.Settings, dir int) {
			if dir == 0 {
				s.ReviewLabels = toggle(s.ReviewLabels, l)
			}
		}})
	}
	for _, t := range hideableTabs {
		items = append(items, settingItem{"Tabs", check(!tabHidden(m.settings, t), tabNames[t]), func(s *store.Settings, dir int) {
			if dir == 0 {
				s.HiddenTabs = toggle(s.HiddenTabs, tabNames[t])
			}
		}})
	}
	return items
}

func (m model) settingsKey(key string) (model, tea.Cmd) {
	items := m.settingItems()
	dir := map[string]int{" ": 0, "enter": 0, "left": -1, "h": -1, "right": 1, "l": 1}
	switch d, ok := dir[key]; {
	case key == "q":
		return m, tea.Quit
	case key == "up" || key == "k":
		m.setSel = max(0, m.setSel-1)
	case key == "down" || key == "j":
		m.setSel = min(len(items)-1, m.setSel+1)
	case ok && m.setSel < len(items):
		next := m.settings
		items[m.setSel].change(&next, d)
		return m.applySettings(next)
	}
	return m, nil
}

func (m model) applySettings(next store.Settings) (model, tea.Cmd) {
	if err := store.WriteJSON(config.SettingsFile, next); err != nil {
		m.err = err
		return m, nil
	}
	before := m.settings
	m.settings = next
	m.buildColumns()
	if next.Language().Code != before.Language().Code {
		m.daily, m.dailyAt, m.dailyW = "", time.Time{}, 0
	}
	if !slices.Equal(next.ReviewLabels, before.ReviewLabels) {
		return m, github.PRsCmd(next.ReviewLabels)
	}
	if !slices.Equal(next.Sources(), before.Sources()) {
		m.loading = true
		return m, m.loadBoardCmd(next.Sources())
	}
	return m, nil
}

func (m model) settingsView(height int) string {
	var lines []string
	section, at := "", 0
	for idx, it := range m.settingItems() {
		if it.section != section {
			section = it.section
			lines = append(lines, "", bold.Render(section))
		}
		cursor := "  "
		if idx == m.setSel {
			cursor, at = lipgloss.NewStyle().Foreground(accent).Render("▸ "), len(lines)
		}
		lines = append(lines, cursor+it.text)
	}
	return strings.Join(lines[max(0, at-height+2):], "\n")
}

type settingItem struct {
	section, text string
	change        func(s *store.Settings, dir int)
}

type statesMsg []tracker.State

var hideableTabs = []tab{tabSessions, tabReviews, tabDaily}
