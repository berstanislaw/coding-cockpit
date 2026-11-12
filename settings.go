package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Settings struct {
	HiddenColumns    []string `json:"hiddenColumns,omitempty"`
	Tickets          []string `json:"tickets,omitempty"`
	DailyLanguage    string   `json:"dailyLanguage,omitempty"`
	NotificationsOff bool     `json:"notificationsOff,omitempty"`
	HiddenTabs       []string `json:"hiddenTabs,omitempty"`
	ReviewLabels     []string `json:"reviewLabels,omitempty"`
	CollapsedGroups  []string `json:"collapsedGroups,omitempty"`
}

type language struct {
	code, name, prompt, today, examples string
	weekdays                            []string
}

type settingItem struct {
	section, text string
	change        func(s *Settings, dir int)
}

type statesMsg []State

var (
	settingsFile = filepath.Join(confDir, "settings.json")
	languages    = []language{
		{"pt", "português", "Brazilian Portuguese", "hoje", `"trabalhei no card que falava de importação de planilhas", "revisei um PR da tela de estoque", "investiguei um bug de permissão no catálogo"`, []string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}},
		{"en", "english", "English", "today", `"worked on the card about spreadsheet imports", "reviewed a PR for the stock screen", "looked into a permission bug in the catalog"`, []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}},
		{"es", "español", "Spanish", "hoy", `"trabajé en la tarjeta sobre importación de planillas", "revisé un PR de la pantalla de stock", "investigué un bug de permisos en el catálogo"`, []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}},
	}
	ticketSources = []struct{ key, label, field string }{
		{"assigned", "assigned to me", "assignee"},
		{"created", "created by me", "creator"},
		{"subscribed", "subscribed to", "subscribers"},
	}
	hideableTabs = []tab{tabSessions, tabReviews, tabDaily}
)

func loadSettings() (Settings, error) {
	var s Settings
	b, err := os.ReadFile(settingsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		return s, fmt.Errorf("%s: %w", settingsFile, err)
	}
	return s, nil
}

func (s Settings) sources() []string {
	var out []string
	for _, src := range ticketSources {
		if slices.Contains(s.Tickets, src.key) {
			out = append(out, src.key)
		}
	}
	if len(out) == 0 {
		return []string{"assigned"}
	}
	return out
}

func (s Settings) language() language {
	for _, l := range languages {
		if l.code == s.DailyLanguage {
			return l
		}
	}
	return languages[0]
}

func (s Settings) tabHidden(t tab) bool {
	return slices.Contains(hideableTabs, t) && slices.Contains(s.HiddenTabs, tabNames[t])
}

func toggle(list []string, v string) []string {
	if i := slices.Index(list, v); i >= 0 {
		return slices.Delete(slices.Clone(list), i, i+1)
	}
	return append(slices.Clone(list), v)
}

func statesCmd(issues []Issue) tea.Cmd {
	var teams []string
	for _, i := range issues {
		if !slices.Contains(teams, i.Team.Key) {
			teams = append(teams, i.Team.Key)
		}
	}
	return func() tea.Msg {
		states, err := fetchStates(teams)
		if err != nil {
			return nil
		}
		return statesMsg(states)
	}
}

func (m model) columnNames() []string {
	var states []State
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

func (m model) hiddenStates() []State {
	out := make([]State, len(m.settings.HiddenColumns))
	for i, name := range m.settings.HiddenColumns {
		out[i] = State{Name: name}
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
		items = append(items, settingItem{"Columns", check(!slices.Contains(m.settings.HiddenColumns, name), name), func(s *Settings, dir int) {
			if dir == 0 {
				s.HiddenColumns = toggle(s.HiddenColumns, name)
			}
		}})
	}
	for _, src := range ticketSources {
		items = append(items, settingItem{"Tickets", check(slices.Contains(m.settings.sources(), src.key), src.label), func(s *Settings, dir int) {
			if t := toggle(s.sources(), src.key); dir == 0 && len(t) > 0 {
				s.Tickets = t
			}
		}})
	}
	items = append(items, settingItem{"Daily", "language  ‹ " + m.settings.language().name + " ›", func(s *Settings, dir int) {
		if dir == 0 {
			dir = 1
		}
		i := slices.IndexFunc(languages, func(l language) bool { return l.code == s.language().code })
		s.DailyLanguage = languages[(i+dir+len(languages))%len(languages)].code
	}})
	items = append(items, settingItem{"Notifications", check(!m.settings.NotificationsOff, "desktop notifications"), func(s *Settings, dir int) {
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
		items = append(items, settingItem{"Team labels for reviews", check(slices.Contains(m.settings.ReviewLabels, l), l), func(s *Settings, dir int) {
			if dir == 0 {
				s.ReviewLabels = toggle(s.ReviewLabels, l)
			}
		}})
	}
	for _, t := range hideableTabs {
		items = append(items, settingItem{"Tabs", check(!m.settings.tabHidden(t), tabNames[t]), func(s *Settings, dir int) {
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

func (m model) applySettings(next Settings) (model, tea.Cmd) {
	if err := writeJSON(settingsFile, next); err != nil {
		m.err = err
		return m, nil
	}
	before := m.settings
	m.settings = next
	m.buildColumns()
	if next.language().code != before.language().code {
		m.daily, m.dailyAt, m.dailyW = "", time.Time{}, 0
	}
	if !slices.Equal(next.ReviewLabels, before.ReviewLabels) {
		return m, prsCmd(next.ReviewLabels)
	}
	if !slices.Equal(next.sources(), before.sources()) {
		m.loading = true
		return m, loadBoardCmd(next.sources())
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
