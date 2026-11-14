package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func run(dir string, stdin []byte, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") && !strings.HasPrefix(e, "LINEAR_API_KEY=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, fmt.Errorf("%s took longer than %s", name, timeout)
	}
	if err != nil {
		return out, fmt.Errorf("%s: %s", name, strings.TrimSpace(stderr.String()+" "+string(out)))
	}
	return out, nil
}

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func livePanes() (live, titles map[string]string, err error) {
	out, err := tmux("list-panes", "-a", "-F", "#{pane_id}\t#{@kta_session}\t#{@kta_title}")
	if err != nil {
		return nil, nil, err
	}
	live, titles = map[string]string{}, map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 || f[1] == "" {
			continue
		}
		live[f[1]] = f[0]
		if len(f) == 3 {
			titles[f[1]] = f[2]
		}
	}
	return live, titles, nil
}

func setTitle(pane, title string) error {
	_, err := tmux("set-option", "-p", "-t", pane, "@kta_title", title)
	return err
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
		return shq(selfExe) + " hook " + shq(title) + " " + event + "; exit 0"
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
	script := "unset CLAUDECODE\nrm -f -- \"$0\"\nunset LINEAR_API_KEY JIRA_URL JIRA_EMAIL JIRA_API_TOKEN TRACKER\nset -a\n. " + shq(envFile) + "\nset +a\nexport KTA_WORKSPACE=" + shq(workspace) + " KTA_SESSION=" + shq(id) + "\n" + strings.Join(cmd, " ") + "\n"
	file := filepath.Join(runDir, id+".sh")
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		return "", err
	}
	pane, err := tmux("new-window", "-d", "-n", "claude", "-c", workDir, "-P", "-F", "#{pane_id}", "sh "+shq(file))
	if err != nil {
		return "", err
	}
	if _, err = tmux("set-option", "-p", "-t", pane, "@kta_session", id); err != nil {
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

func switchWorkspace(name string) error {
	live, _, err := livePanes()
	if err != nil {
		return err
	}
	for _, pane := range live {
		killPane(pane)
	}
	bin := shq(selfExe)
	_, err = tmux("respawn-pane", "-k", "-t", selfPane, "-e", "KTA_WORKSPACE="+name, "trap : INT; "+bin+" check || export KTA_WORKSPACE="+shq(workspace)+"; exec "+bin)
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
