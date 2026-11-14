package claude

import (
	"encoding/json"
	"kta/internal/config"
	"kta/internal/shell"
	"kta/internal/tmux"
	"kta/internal/tracker"
	"os"
	"path/filepath"
	"strings"
)

func hookSettings(id, title string) string {
	file := shell.Quote(filepath.Join(config.StatusDir, id))
	hook := func(cmd string) []map[string]any {
		return []map[string]any{{"hooks": []map[string]string{{"type": "command", "command": cmd}}}}
	}
	set := func(state string) string { return "printf " + state + " > " + file }
	notify := func(event string) string {
		return shell.Quote(config.SelfExe) + " hook " + shell.Quote(title) + " " + event + "; exit 0"
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

func Launch(id, title string, args ...string) (string, error) {
	cmd := []string{"exec", "claude"}
	for _, a := range append([]string{"--settings", hookSettings(id, title)}, args...) {
		cmd = append(cmd, shell.Quote(a))
	}
	script := "unset CLAUDECODE\nrm -f -- \"$0\"\nunset " + strings.Join(tracker.EnvNames(), " ") + " TRACKER\nset -a\n. " + shell.Quote(config.EnvFile) + "\nset +a\nexport KTA_WORKSPACE=" + shell.Quote(config.Workspace) + " KTA_SESSION=" + shell.Quote(id) + "\n" + strings.Join(cmd, " ") + "\n"
	file := filepath.Join(config.RunDir, id+".sh")
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		return "", err
	}
	pane, err := tmux.Run("new-window", "-d", "-n", "claude", "-c", config.WorkDir, "-P", "-F", "#{pane_id}", "sh "+shell.Quote(file))
	if err != nil {
		return "", err
	}
	if _, err = tmux.Run("set-option", "-p", "-t", pane, "@kta_session", id); err != nil {
		return pane, err
	}
	return pane, tmux.SetTitle(pane, title)
}
