package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	stepTracker = iota
	stepAccount
	stepFolder
	stepSave
)

var (
	trackers = []struct{ key, name, about string }{
		{"linear", "Linear", "linear.app, with a personal API key"},
		{"jira", "Jira", "Jira Cloud, with your email and an API token"},
	}
	stepNames = []string{"tracker", "account", "folder", "save"}
	secretEnv = map[string]bool{"LINEAR_API_KEY": true, "JIRA_API_TOKEN": true}
)

type accountField struct {
	env, label, hint string
}

var accountFields = map[string][]accountField{
	"linear": {{"LINEAR_API_KEY", "API key", "Linear → Settings → Account → Security & access → Personal API keys"}},
	"jira": {
		{"JIRA_URL", "Site", "the address of your Jira, like acme.atlassian.net (acme works too)"},
		{"JIRA_EMAIL", "Email", "the email you sign in to Atlassian with"},
		{"JIRA_API_TOKEN", "API token", "id.atlassian.com → Security → Create and manage API tokens"},
	},
}

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

func newWizard(old map[string]string) wizard {
	w := wizard{old: old}
	if old["TRACKER"] == "jira" || (old["TRACKER"] == "" && old["JIRA_URL"] != "") {
		w.choice = 1
	}
	w.folder = textinput.New()
	w.folder.Prompt = ""
	dir := old["WORKSPACE_DIR"]
	if dir == "" {
		dir, _ = os.Getwd()
		if rest, ok := strings.CutPrefix(dir, home+"/"); ok {
			dir = "~/" + rest
		}
	}
	w.folder.SetValue(dir)
	return w
}

func (w wizard) tracker() string { return trackers[w.choice].key }

func (w *wizard) buildInputs() {
	fields := accountFields[w.tracker()]
	w.inputs = make([]textinput.Model, len(fields))
	for i, f := range fields {
		in := textinput.New()
		in.Prompt = fmt.Sprintf("%-10s", f.label)
		in.Width = 40
		value := w.old[f.env]
		switch {
		case secretEnv[f.env]:
			in.EchoMode, in.EchoCharacter = textinput.EchoPassword, '•'
			in.Placeholder = "paste it here, it won't show"
			if value != "" {
				in.Placeholder = "enter keeps the current one"
			}
		case f.env == "JIRA_URL":
			in.SetValue(strings.TrimPrefix(value, "https://"))
			in.Placeholder = "acme.atlassian.net"
		default:
			in.SetValue(value)
		}
		w.inputs[i] = in
	}
	w.field, w.built = 0, w.tracker()
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
	for i, f := range accountFields[w.tracker()] {
		value := strings.TrimSpace(w.inputs[i].Value())
		if value == "" && secretEnv[f.env] {
			value = w.old[f.env]
		}
		if f.env == "JIRA_URL" {
			value = jiraSite(value)
		}
		v[f.env] = value
	}
	return v
}

func (w wizard) env() map[string]string {
	env := maps.Clone(w.old)
	if env == nil {
		env = map[string]string{}
	}
	for _, fields := range accountFields {
		for _, f := range fields {
			delete(env, f.env)
		}
	}
	maps.Copy(env, w.values())
	env["TRACKER"] = w.tracker()
	env["WORKSPACE_DIR"] = strings.TrimSpace(w.folder.Value())
	return env
}

func writeEnv(env map[string]string) error {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(&b, "%s=%s\n", k, shq(env[k]))
	}
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(envFile, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(envFile, 0o600)
}

func checkAccount(t string, v map[string]string) tea.Cmd {
	return func() tea.Msg {
		if t == "jira" {
			jiraURL, jiraEmail, jiraToken = v["JIRA_URL"], v["JIRA_EMAIL"], v["JIRA_API_TOKEN"]
			name, err := jiraMyself()
			return accountMsg{name, err}
		}
		apiKey = v["LINEAR_API_KEY"]
		name, err := linearViewer()
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
			w.choice = min(len(trackers)-1, w.choice+1)
		case "enter":
			if w.built != w.tracker() {
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
			for i, f := range accountFields[w.tracker()] {
				if v[f.env] == "" {
					w.field, w.err = i, f.label+" is missing"
					return w, w.focus()
				}
			}
			w.checking, w.err, w.account = true, "", ""
			return w, checkAccount(w.tracker(), v)
		}
		var cmd tea.Cmd
		w.inputs[w.field], cmd = w.inputs[w.field].Update(k)
		return w, cmd
	case stepFolder:
		if key == "enter" {
			dir := expandHome(strings.TrimSpace(w.folder.Value()))
			if !isDir(dir) {
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
			if err := writeEnv(w.env()); err != nil {
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
	lines := []string{bold.Render(title) + dim.Render(" · "+workspaceLabel(workspace)), strings.Join(progress, "   "), ""}
	var keys string
	switch w.step {
	case stepTracker:
		lines = append(lines, "Where do your tickets live?", "")
		for i, t := range trackers {
			if i == w.choice {
				lines = append(lines, accentStyle.Render(fmt.Sprintf("▸ %-8s", t.name))+dim.Render(t.about))
			} else {
				lines = append(lines, fmt.Sprintf("  %-8s", t.name)+dim.Render(t.about))
			}
		}
		keys = "↑↓ choose · enter next · esc cancel"
	case stepAccount:
		lines = append(lines, "Connect your "+trackers[w.choice].name+" account", "")
		for _, in := range w.inputs {
			lines = append(lines, in.View())
		}
		lines = append(lines, "", dim.Render(accountFields[w.tracker()][w.field].hint))
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
		where := trackers[w.choice].name
		if w.tracker() == "jira" {
			where += "  " + dim.Render(strings.TrimPrefix(v["JIRA_URL"], "https://"))
		}
		folder := w.folder.Value()
		saved := envFile
		if rest, ok := strings.CutPrefix(saved, home+"/"); ok {
			saved = "~/" + rest
		}
		lines = append(lines,
			dim.Render("Tracker   ")+where,
			dim.Render("Account   ")+w.account,
			dim.Render("Folder    ")+folder,
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

func runWizard() error {
	final, err := tea.NewProgram(newWizard(readEnv(envFile)), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if w, ok := final.(wizard); !ok || !w.saved {
		return errors.New("setup cancelled")
	}
	return loadConfig()
}
