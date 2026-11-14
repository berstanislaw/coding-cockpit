package claude

import (
	"encoding/json"
	"kta/internal/config"
	"kta/internal/shell"
	"kta/internal/store"
	"kta/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionName(t *testing.T) {
	cases := map[string]string{
		"":                         "session 3",
		"  fix the   import\nmore": "fix the import",
		"adjust the spreadsheet so we can start importing it": "adjust the spreadsheet so we can start…",
	}
	for in, want := range cases {
		if got := SessionName(in, 3); got != want {
			t.Errorf("sessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHook(t *testing.T) {
	dir := testenv.UseTemp(t)
	store.WriteJSON(config.SettingsFile, store.Settings{NotificationsOff: true})
	status := func() string { b, _ := os.ReadFile(filepath.Join(dir, "s1")); return string(b) }
	cases := []struct{ before, event, input, want string }{
		{"working", "stop", `{"session_id":"s1","background_tasks":[{"type":"subagent"},{"type":"shell"},{"type":"subagent"}]}`, "background 2 agents, 1 shell"},
		{"working", "stop", `{"session_id":"s1"}`, "waiting"},
		{"background 1 agent", "notification", `{"session_id":"s1","notification_type":"idle_prompt"}`, "background 1 agent"},
		{"working", "notification", `{"session_id":"s1","notification_type":"idle_prompt"}`, "waiting"},
		{"working", "notification", `{"session_id":"s1","notification_type":"permission_prompt","message":"Claude needs permission"}`, "waiting"},
		{"working", "stop", `not json`, "working"},
	}
	for _, c := range cases {
		os.WriteFile(filepath.Join(dir, "s1"), []byte(c.before), 0o600)
		RunHook("title", c.event, strings.NewReader(c.input))
		if got := status(); got != c.want {
			t.Errorf("%s %s from %q: status %q, want %q", c.event, c.input, c.before, got, c.want)
		}
	}
	RunHook("title", "stop", strings.NewReader(`{"session_id":"../../escape"}`))
	if _, err := os.Stat(filepath.Join(dir, "escape")); err != nil {
		t.Errorf("session id with a path should stay inside the status folder: %v", err)
	}
}

func TestTranscript(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(file, []byte(`{"type":"user","message":{"content":"question"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"first reply"},{"type":"tool_use"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"second reply"}]}}`), 0o600)
	var out strings.Builder
	if err := PrintTranscript(file, &out); err != nil || out.String() != "first reply\n\nsecond reply\n\n" {
		t.Errorf("printTranscript = %q, %v", out.String(), err)
	}
}

func TestNotifyTitle(t *testing.T) {
	cases := map[string]string{
		"KAN-1118 · fix the glossary search":    "KAN-1118",
		"KAN-1115+KAN-1116+KAN-1118 · together": "KAN-1115 +2",
		"shop-web#1990 · answer the threads":    "shop-web#1990",
		"quick question about go":               "quick question about go",
	}
	for in, want := range cases {
		if got := notifyTitle(in); got != want {
			t.Errorf("notifyTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHookQuoting(t *testing.T) {
	dir := testenv.UseTemp(t)
	out, exe := filepath.Join(dir, "args"), filepath.Join(dir, "fake-kta")
	os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shell.Quote(out)+"\n"), 0o700)
	old := config.SelfExe
	config.SelfExe = exe
	t.Cleanup(func() { config.SelfExe = old })
	title := "it's $(touch pwned) `touch pwned` \"x\"\nnext"
	var s struct {
		Hooks map[string][]struct{ Hooks []struct{ Command string } }
	}
	if err := json.Unmarshal([]byte(hookSettings("s1", title)), &s); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "Stop"} {
		cmd := exec.Command("sh", "-c", s.Hooks[event][0].Hooks[0].Command)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s hook: %v", event, err)
		}
	}
	if b, _ := os.ReadFile(out); string(b) != "hook\n"+title+"\nstop\n" {
		t.Errorf("the hook got %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(config.StatusDir, "s1")); string(b) != "waiting" {
		t.Errorf("status = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("the title ran as shell code")
	}
}

func TestMain(m *testing.M) { testenv.Main(m) }
