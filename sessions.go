package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const maxPromptBytes = 120_000

var (
	home, _      = os.UserHomeDir()
	appDir       = func() string { exe, _ := os.Executable(); return filepath.Dir(exe) }()
	envFile      = filepath.Join(appDir, ".env")
	notifyScript = filepath.Join(appDir, "notify.sh")
	confDir      = filepath.Join(home, ".config/linear-board")
	sessionsFile = filepath.Join(confDir, "sessions.json")
	statusDir    = filepath.Join(confDir, "status")
	runDir       = filepath.Join(confDir, "run")
	selfPane     = os.Getenv("TMUX_PANE")

	workDir, claudeSessionsDir string
)

func loadConfig() error {
	if b, err := os.ReadFile(envFile); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
			if k = strings.TrimSpace(k); ok && !strings.HasPrefix(k, "#") && os.Getenv(k) == "" {
				os.Setenv(k, strings.Trim(strings.TrimSpace(v), `"'`))
			}
		}
	}
	apiKey, workDir = os.Getenv("LINEAR_API_KEY"), os.Getenv("WORKSPACE_DIR")
	if apiKey == "" || workDir == "" {
		return fmt.Errorf("set LINEAR_API_KEY and WORKSPACE_DIR in %s (cp .env.example .env)", envFile)
	}
	if rest, ok := strings.CutPrefix(workDir, "~/"); ok {
		workDir = filepath.Join(home, rest)
	}
	if st, err := os.Stat(workDir); err != nil || !st.IsDir() {
		return fmt.Errorf("WORKSPACE_DIR %s is not a folder", workDir)
	}
	claudeSessionsDir = filepath.Join(home, ".claude/projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(workDir, "-"))
	return nil
}

type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

func loadSessions() (map[string][]Session, error) {
	m := map[string][]Session{}
	b, err := os.ReadFile(sessionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func saveSessions(m map[string][]Session) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(sessionsFile+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(sessionsFile+".tmp", sessionsFile)
}

func mutateSessions(change func(map[string][]Session)) (map[string][]Session, error) {
	m, err := loadSessions()
	if err != nil {
		return nil, err
	}
	change(m)
	return m, saveSessions(m)
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func livePanes() (live map[string]string, untitled []string, err error) {
	out, err := tmux("list-panes", "-a", "-F", "#{pane_id}\t#{@lb_session}\t#{@lb_title}")
	if err != nil {
		return nil, nil, err
	}
	live = map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 || f[1] == "" {
			continue
		}
		live[f[1]] = f[0]
		if len(f) < 3 || f[2] == "" {
			untitled = append(untitled, f[1])
		}
	}
	return live, untitled, nil
}

func setTitle(pane, title string) error {
	_, err := tmux("set-option", "-p", "-t", pane, "@lb_title", title)
	return err
}

func readStatus() map[string]string {
	status := map[string]string{}
	entries, _ := os.ReadDir(statusDir)
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join(statusDir, e.Name())); err == nil {
			status[e.Name()] = strings.TrimSpace(string(b))
		}
	}
	return status
}

func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func hookSettings(id, title string) string {
	file := shq(filepath.Join(statusDir, id))
	hook := func(cmd string) []map[string]any {
		return []map[string]any{{"hooks": []map[string]string{{"type": "command", "command": cmd}}}}
	}
	set := func(state string) string { return "printf " + state + " > " + file }
	notify := func(event string) string {
		return "sh " + shq(notifyScript) + " " + shq(title) + " " + event + "; exit 0"
	}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart":     hook(set("waiting")),
		"UserPromptSubmit": hook(set("working")),
		"PostToolUse":      hook(set("working")),
		"Notification":     hook(notify("notification")),
		"Stop":             hook(notify("stop")),
	}})
	return string(b)
}

func launch(id, title string, args ...string) (string, error) {
	cmd := []string{"exec", "claude"}
	for _, a := range append([]string{"--settings", hookSettings(id, title)}, args...) {
		cmd = append(cmd, shq(a))
	}
	script := "unset CLAUDECODE\nrm -f -- \"$0\"\n" + strings.Join(cmd, " ") + "\n"
	file := filepath.Join(runDir, id+".sh")
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		return "", err
	}
	pane, err := tmux("new-window", "-d", "-n", "claude", "-c", workDir, "-P", "-F", "#{pane_id}", "sh "+shq(file))
	if err != nil {
		return "", err
	}
	if _, err = tmux("set-option", "-p", "-t", pane, "@lb_session", id); err != nil {
		return pane, err
	}
	return pane, setTitle(pane, title)
}

func rightPane() string {
	out, err := tmux("list-panes", "-t", selfPane, "-F", "#{pane_id}")
	if err != nil {
		return ""
	}
	for _, p := range strings.Fields(out) {
		if p != selfPane {
			return p
		}
	}
	return ""
}

func keepLeft() {
	out, err := tmux("display", "-p", "-t", selfPane, "#{pane_index} #{window_id}")
	if idx, win, ok := strings.Cut(out, " "); err == nil && ok && idx != "0" {
		tmux("swap-pane", "-d", "-s", selfPane, "-t", win+".0")
	}
}

func hideRight() error {
	if cur := rightPane(); cur != "" {
		_, err := tmux("break-pane", "-d", "-s", cur, "-n", "claude")
		return err
	}
	return nil
}

func showRight(pane string) error {
	if rightPane() == pane {
		return nil
	}
	if err := hideRight(); err != nil {
		return err
	}
	_, err := tmux("join-pane", "-h", "-d", "-l", "50%", "-s", pane, "-t", selfPane)
	return err
}

func focusPane(pane string) error {
	_, err := tmux("select-pane", "-t", pane)
	return err
}

func killPane(pane string) error {
	_, err := tmux("kill-pane", "-t", pane)
	return err
}

func sessionName(request string, n int) string {
	first, _, _ := strings.Cut(strings.TrimSpace(request), "\n")
	line := strings.Join(strings.Fields(first), " ")
	if line == "" {
		return fmt.Sprintf("session %d", n)
	}
	r := []rune(line)
	if len(r) <= 40 {
		return line
	}
	cut := string(r[:40])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

func previousSessions(list []Session, live map[string]string) []string {
	var lines []string
	for _, s := range list {
		file := filepath.Join(claudeSessionsDir, s.ID+".jsonl")
		if _, err := os.Stat(file); err != nil {
			continue
		}
		running := ""
		if _, ok := live[s.ID]; ok {
			running = ", still running"
		}
		lines = append(lines, fmt.Sprintf(`- "%s" (%.10s%s): %s`, s.Name, s.CreatedAt, running, file))
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{
		"## Previous sessions on this ticket",
		"Other Claude sessions already worked on this ticket. If you need what was done or decided there, read only the replies (the full file is large):",
		"`jq -r 'select(.type==\"assistant\") | .message.content[]? | select(.type==\"text\") | .text' <file>`",
	}, append(lines, "")...)
}

func buildPrompt(i Issue, request string, previous []string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		request = "Read the ticket below and tell me how you want to proceed."
	}
	labels := make([]string, len(i.Labels.Nodes))
	for k, l := range i.Labels.Nodes {
		labels[k] = l.Name
	}
	status := fmt.Sprintf("Status: %s · Team: %s", i.State.Name, i.Team.Key)
	if len(labels) > 0 {
		status += " · Labels: " + strings.Join(labels, ", ")
	}
	description := i.Description
	if description == "" {
		description = "(no description)"
	}
	parts := append([]string{request, ""}, previous...)
	parts = append(parts, fmt.Sprintf("# Linear ticket %s: %s", i.Identifier, i.Title), i.URL, status, "", "## Description", description)
	if len(i.Comments.Nodes) > 0 {
		parts = append(parts, "", "## Comments")
		for _, c := range i.Comments.Nodes {
			name := "integration"
			if c.User != nil {
				name = c.User.Name
			}
			parts = append(parts, fmt.Sprintf("- %s (%.10s): %s", name, c.CreatedAt, c.Body))
		}
	}
	text := strings.Join(parts, "\n")
	if len(text) > maxPromptBytes {
		text = strings.ToValidUTF8(text[:maxPromptBytes], "")
	}
	return text
}
