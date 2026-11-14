package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type workspaceInfo struct {
	name, dir string
	fresh     bool
}

var workspaceName = regexp.MustCompile(`^[a-z0-9_-]+$`)

func workspaceLabel(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

func listWorkspaces() []workspaceInfo {
	names := []string{""}
	entries, _ := os.ReadDir(filepath.Join(rootDir, "workspaces"))
	for _, e := range entries {
		if e.IsDir() && workspaceName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	list := make([]workspaceInfo, len(names))
	for i, n := range names {
		dir := readEnv(filepath.Join(workspaceDir(n), "env"))["WORKSPACE_DIR"]
		if rest, ok := strings.CutPrefix(dir, home+"/"); ok {
			dir = "~/" + rest
		}
		list[i] = workspaceInfo{name: n, dir: dir}
	}
	return list
}

func pickRows(list []workspaceInfo, filter string) []workspaceInfo {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var rows []workspaceInfo
	exact := false
	for _, w := range list {
		if strings.Contains(workspaceLabel(w.name), filter) {
			rows = append(rows, w)
		}
		exact = exact || workspaceLabel(w.name) == filter
	}
	if filter != "" && !exact && workspaceName.MatchString(filter) {
		rows = append(rows, workspaceInfo{name: filter, fresh: true})
	}
	return rows
}

func (m model) openPicker() (model, tea.Cmd) {
	m.picking, m.pickSel, m.workspaces = true, 0, listWorkspaces()
	m.pick.Reset()
	return m, m.pick.Focus()
}

func (m model) pickKey(k tea.KeyMsg) (model, tea.Cmd) {
	rows := pickRows(m.workspaces, m.pick.Value())
	switch k.String() {
	case "esc", "alt+w":
		m.picking = false
		return m, nil
	case "up", "ctrl+p", "ctrl+k", "shift+tab":
		m.pickSel = max(0, m.pickSel-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j", "tab":
		m.pickSel = max(0, min(len(rows)-1, m.pickSel+1))
		return m, nil
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		w := rows[min(m.pickSel, len(rows)-1)]
		m.picking = false
		if w.name != workspace || w.fresh {
			if err := switchWorkspace(w.name); err != nil {
				m.err = err
			}
		}
		return m, nil
	}
	before := m.pick.Value()
	var cmd tea.Cmd
	m.pick, cmd = m.pick.Update(k)
	if m.pick.Value() != before {
		m.pickSel = 0
	}
	return m, cmd
}

func (m model) workspacesView(height int) string {
	rows := pickRows(m.workspaces, m.pick.Value())
	sel := min(m.pickSel, len(rows)-1)
	w := max(20, min(72, m.w-4))
	lines := []string{bold.Render("Workspaces"), "", m.pick.View(), ""}
	for i, r := range rows {
		name, note := workspaceLabel(r.name), dim.Render(r.dir)
		switch {
		case r.fresh:
			name, note = "+ "+name, dim.Render("new workspace, asks for its key and folder")
		case r.name == workspace:
			note = markStyle.Render("open") + "  " + note
		}
		cursor := "  "
		if i == sel {
			cursor, name = lipgloss.NewStyle().Foreground(focusColor).Render("▸ "), lipgloss.NewStyle().Bold(true).Foreground(focusColor).Render(name)
		}
		lines = append(lines, ansi.Truncate(cursor+name+"  "+note, w-2, "…"))
	}
	if len(rows) == 0 {
		lines = append(lines, dim.Render("  no workspace matches; names use a-z, 0-9, - and _"))
	}
	if n := len(m.live); n > 0 {
		lines = append(lines, "", stateStyle["waiting"].Render(fmt.Sprintf("opening another workspace stops %s", plural(n, "running Claude session"))))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(focusColor).Padding(0, 1).Width(w).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.w, height, lipgloss.Center, lipgloss.Center, box)
}
