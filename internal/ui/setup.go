package ui

import (
	"errors"
	"fmt"
	"kta/internal/config"
	"kta/internal/tracker"
	"maps"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func newWizard(old map[string]string) wizard {
	w := wizard{old: old}
	kind := tracker.KeyOf(func(name string) string { return old[name] })
	for i, t := range tracker.All {
		if t.Key == kind {
			w.choice = i
		}
	}
	w.folder = textinput.New()
	w.folder.Prompt = ""
	dir := old["WORKSPACE_DIR"]
	if dir == "" {
		dir, _ = os.Getwd()
		if rest, ok := strings.CutPrefix(dir, config.Home+"/"); ok {
			dir = "~/" + rest
		}
	}
	w.folder.SetValue(dir)
	return w
}

func (w wizard) kind() tracker.Kind { return tracker.All[w.choice] }

func (w *wizard) buildInputs() {
	fields := w.kind().Fields
	w.inputs = make([]textinput.Model, len(fields))
	for i, f := range fields {
		in := textinput.New()
		in.Prompt = fmt.Sprintf("%-10s", f.Label)
		in.Width = 40
		value := w.old[f.Env]
		if f.Secret {
			in.EchoMode, in.EchoCharacter = textinput.EchoPassword, '•'
			in.Placeholder = "paste it here, it won't show"
			if value != "" {
				in.Placeholder = "enter keeps the current one"
			}
		} else {
			in.SetValue(strings.TrimPrefix(value, "https://"))
			in.Placeholder = f.Placeholder
		}
		w.inputs[i] = in
	}
	w.field, w.built = 0, w.kind().Key
}

func (w *wizard) focus() tea.Cmd {
	for i := range w.inputs {
		w.inputs[i].Blur()
	}
	w.folder.Blur()
	switch w.step {
	case stepAccount:
		return w.inputs[w.field].Focus()
	case stepFolder:
		return w.folder.Focus()
	}
	return nil
}

func (w wizard) values() map[string]string {
	v := map[string]string{}
	for i, f := range w.kind().Fields {
		value := f.Clean(w.inputs[i].Value())
		if value == "" && f.Secret {
			value = w.old[f.Env]
		}
		v[f.Env] = value
	}
	return v
}

func (w wizard) env() map[string]string {
	env := maps.Clone(w.old)
	if env == nil {
		env = map[string]string{}
	}
	for _, name := range tracker.EnvNames() {
		delete(env, name)
	}
	maps.Copy(env, w.values())
	env["TRACKER"] = w.kind().Key
	env["WORKSPACE_DIR"] = strings.TrimSpace(w.folder.Value())
	return env
}

func checkAccount(kind tracker.Kind, v map[string]string) tea.Cmd {
	return func() tea.Msg {
		t, err := tracker.Connect(kind, func(name string) string { return v[name] })
		if err != nil {
			return accountMsg{err: err}
		}
		name, err := t.Me()
		return accountMsg{name, err}
	}
}

func (w wizard) Init() tea.Cmd { return textinput.Blink }

func (w wizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w.w, w.h = msg.Width, msg.Height
		return w, nil
	case accountMsg:
		w.checking = false
		if msg.err != nil {
			w.err = msg.err.Error()
			return w, nil
		}
		w.account, w.step = msg.name, stepFolder
		return w, w.focus()
	case tea.KeyMsg:
		return w.key(msg)
	}
	return w, nil
}

func (w wizard) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return w, tea.Quit
	}
	if w.checking {
		return w, nil
	}
	if key == "esc" {
		if w.step == stepTracker {
			return w, tea.Quit
		}
		w.step, w.err = w.step-1, ""
		return w, w.focus()
	}
	switch w.step {
	case stepTracker:
		switch key {
		case "up", "k", "shift+tab":
			w.choice = max(0, w.choice-1)
		case "down", "j", "tab":
			w.choice = min(len(tracker.All)-1, w.choice+1)
		case "enter":
			if w.built != w.kind().Key {
				w.buildInputs()
				w.account = ""
			}
			w.step, w.err = stepAccount, ""
			return w, w.focus()
		}
		return w, nil
	case stepAccount:
		switch key {
		case "tab", "down":
			w.field = (w.field + 1) % len(w.inputs)
			return w, w.focus()
		case "shift+tab", "up":
			w.field = (w.field + len(w.inputs) - 1) % len(w.inputs)
			return w, w.focus()
		case "enter":
			v := w.values()
			for i, f := range w.kind().Fields {
				if v[f.Env] == "" {
					w.field, w.err = i, f.Label+" is missing"
					return w, w.focus()
				}
			}
			w.checking, w.err, w.account = true, "", ""
			return w, checkAccount(w.kind(), v)
		}
		var cmd tea.Cmd
		w.inputs[w.field], cmd = w.inputs[w.field].Update(k)
		return w, cmd
	case stepFolder:
		if key == "enter" {
			dir := config.ExpandHome(strings.TrimSpace(w.folder.Value()))
			if !config.IsDir(dir) {
				w.err = dir + " is not a folder"
				return w, nil
			}
			w.step, w.err = stepSave, ""
			return w, w.focus()
		}
		var cmd tea.Cmd
		w.folder, cmd = w.folder.Update(k)
		return w, cmd
	case stepSave:
		if key == "enter" {
			if err := config.WriteEnv(w.env()); err != nil {
				w.err = err.Error()
				return w, nil
			}
			w.saved = true
			return w, tea.Quit
		}
	}
	return w, nil
}

func (w wizard) View() string {
	if w.w == 0 {
		return ""
	}
	width := max(30, min(78, w.w-4))
	accentStyle := lipgloss.NewStyle().Bold(true).Foreground(focusColor)
	title := "New workspace"
	if len(w.old) > 0 {
		title = "Workspace settings"
	}
	var progress []string
	for i, name := range stepNames {
		switch {
		case i < w.step:
			progress = append(progress, markStyle.Render("✓ "+name))
		case i == w.step:
			progress = append(progress, accentStyle.Render("● "+name))
		default:
			progress = append(progress, dim.Render("○ "+name))
		}
	}
	lines := []string{bold.Render(title) + dim.Render(" · "+workspaceLabel(config.Workspace)), strings.Join(progress, "   "), ""}
	var keys string
	switch w.step {
	case stepTracker:
		lines = append(lines, "Where do your tickets live?", "")
		for i, t := range tracker.All {
			if i == w.choice {
				lines = append(lines, accentStyle.Render(fmt.Sprintf("▸ %-8s", t.Name))+dim.Render(t.About))
			} else {
				lines = append(lines, fmt.Sprintf("  %-8s", t.Name)+dim.Render(t.About))
			}
		}
		keys = "↑↓ choose · enter next · esc cancel"
	case stepAccount:
		lines = append(lines, "Connect your "+w.kind().Name+" account", "")
		for _, in := range w.inputs {
			lines = append(lines, in.View())
		}
		lines = append(lines, "", dim.Render(w.kind().Fields[w.field].Hint))
		switch {
		case w.checking:
			lines = append(lines, "", stateStyle["waiting"].Render("checking…"))
		case w.account != "":
			lines = append(lines, "", markStyle.Render("✓ connected as "+w.account))
		}
		keys = "tab next field · enter connect · esc back"
	case stepFolder:
		lines = append(lines, "Where should Claude sessions start?", dim.Render("usually the repo or the workspace you work in"), "", w.folder.View(), "", markStyle.Render("✓ connected as "+w.account))
		keys = "enter next · esc back"
	case stepSave:
		v := w.values()
		saved := config.EnvFile
		if rest, ok := strings.CutPrefix(saved, config.Home+"/"); ok {
			saved = "~/" + rest
		}
		lines = append(lines, dim.Render("Tracker   ")+w.kind().Name)
		for _, f := range w.kind().Fields {
			if !f.Secret {
				lines = append(lines, dim.Render(fmt.Sprintf("%-10s", f.Label))+strings.TrimPrefix(v[f.Env], "https://"))
			}
		}
		lines = append(lines,
			dim.Render("Account   ")+w.account,
			dim.Render("Folder    ")+w.folder.Value(),
			"",
			dim.Render("Saved to "+saved+", readable only by you."),
			dim.Render("Claude sessions on this board get them as environment variables."))
		keys = "enter save and open the board · esc back"
	}
	if w.err != "" {
		lines = append(lines, "", errStyle.Render(w.err))
	}
	lines = append(lines, "", dim.Render(keys))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(focusColor).Padding(0, 2).Width(width).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(w.w, w.h, lipgloss.Center, lipgloss.Center, box)
}

func RunWizard() (tracker.Client, error) {
	final, err := tea.NewProgram(newWizard(config.ReadEnv(config.EnvFile)), tea.WithAltScreen()).Run()
	if err != nil {
		return tracker.Client{}, err
	}
	if w, ok := final.(wizard); !ok || !w.saved {
		return tracker.Client{}, errors.New("setup cancelled")
	}
	return config.Load()
}

const (
	stepTracker = iota
	stepAccount
	stepFolder
	stepSave
)

var (
	stepNames = []string{"tracker", "account", "folder", "save"}
)

type accountMsg struct {
	name string
	err  error
}

type wizard struct {
	step, choice, field int
	inputs              []textinput.Model
	folder              textinput.Model
	old                 map[string]string
	account, err, built string
	checking, saved     bool
	w, h                int
}
