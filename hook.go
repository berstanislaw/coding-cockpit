package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type hookInput struct {
	SessionID        string `json:"session_id"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	BackgroundTasks  []struct {
		Type string `json:"type"`
	} `json:"background_tasks"`
}

func runHook(title, event string, r io.Reader) {
	var in hookInput
	if json.NewDecoder(r).Decode(&in) != nil || in.SessionID == "" {
		return
	}
	file := filepath.Join(statusDir, filepath.Base(in.SessionID))
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
	if s, _ := loadSettings(); s.NotificationsOff || paneInView() {
		return
	}
	notify(notifyTitle(title), body)
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

func paneInView() bool {
	out, err := tmux("display", "-p", "-t", os.Getenv("TMUX_PANE"), "#{window_active}#{m:*focused*,#{client_flags}}")
	return err == nil && out == "11"
}

func notify(title, body string) {
	if _, err := exec.LookPath("notify-send"); err == nil {
		exec.Command("notify-send", "-a", "kta", title, body).Run()
		return
	}
	exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, body).Run()
}

func printTranscript(file string, w io.Writer) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var l struct {
			Type    string
			Message struct{ Content json.RawMessage }
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "assistant" {
			if text := contentText(l.Message.Content); text != "" {
				fmt.Fprintf(w, "%s\n\n", text)
			}
		}
		if err != nil {
			return nil
		}
	}
}
