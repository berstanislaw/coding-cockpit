package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"kta/internal/config"
	"kta/internal/shell"
	"kta/internal/store"
	"kta/internal/tmux"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func RunHook(title, event string, r io.Reader) {
	var in hookInput
	if json.NewDecoder(r).Decode(&in) != nil || in.SessionID == "" {
		return
	}
	file := filepath.Join(config.StatusDir, filepath.Base(in.SessionID))
	body := "finished, waiting for you"
	switch {
	case event == "stop" && len(in.BackgroundTasks) > 0:
		os.WriteFile(file, []byte("background "+running(in)), 0o600)
		return
	case event == "stop":
	case in.NotificationType == "idle_prompt":
		if b, _ := os.ReadFile(file); !strings.HasPrefix(string(b), "background") {
			os.WriteFile(file, []byte("waiting"), 0o600)
		}
		return
	case in.Message != "":
		body = in.Message
	default:
		body = "needs your attention"
	}
	os.WriteFile(file, []byte("waiting"), 0o600)
	if s, _ := store.LoadSettings(); s.NotificationsOff || tmux.PaneInView() {
		return
	}
	shell.Notify(liveTitle(in.SessionID, title), body)
}

func liveTitle(id, title string) string {
	sessions, groups, err := store.LoadState()
	if t := store.SessionTitle(sessions, groups, id); err == nil && t != "" {
		title = t
	}
	return notifyTitle(title)
}

func notifyTitle(title string) string {
	key, _, found := strings.Cut(title, " · ")
	if !found {
		return title
	}
	if keys := strings.Split(key, "+"); len(keys) > 1 {
		return fmt.Sprintf("%s +%d", keys[0], len(keys)-1)
	}
	return key
}

func running(in hookInput) string {
	count := map[string]int{}
	for _, t := range in.BackgroundTasks {
		name := t.Type
		if name == "subagent" {
			name = "agent"
		}
		count[name]++
	}
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(count)) {
		part := fmt.Sprintf("%d %s", count[name], name)
		if count[name] > 1 {
			part += "s"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

type hookInput struct {
	SessionID        string `json:"session_id"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	BackgroundTasks  []struct {
		Type string `json:"type"`
	} `json:"background_tasks"`
}
