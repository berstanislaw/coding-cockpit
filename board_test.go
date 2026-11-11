package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPRs(t *testing.T) {
	cases := []struct{ url, tag, label string }{
		{"https://github.com/acme/shop-api/pull/1782", "A#1782", "shop-api #1782"},
		{"https://github.com/acme/shop-web/pull/2210/files", "W#2210", "shop-web #2210"},
		{"https://github.com/acme/docs/pull/347", "D#347", "docs #347"},
		{"https://github.com/acme/trailing-/pull/9", "T#9", "trailing- #9"},
		{"https://example.com/x", "PR", "https://example.com/x"},
	}
	for _, c := range cases {
		if got := prTag(c.url); got != c.tag {
			t.Errorf("prTag(%s) = %s, want %s", c.url, got, c.tag)
		}
		if got := prLabel(c.url); got != c.label {
			t.Errorf("prLabel(%s) = %s, want %s", c.url, got, c.label)
		}
	}
	var i Issue
	for _, u := range []string{"https://github.com/acme/api/pull/1/files", "https://linear.app/x", "https://github.com/acme/web/pull/2"} {
		i.Attachments.Nodes = append(i.Attachments.Nodes, struct{ URL string }{u})
	}
	want := []string{"https://github.com/acme/api/pull/1", "https://github.com/acme/web/pull/2"}
	if got := issuePRs(i); !slices.Equal(got, want) {
		t.Errorf("issuePRs = %v, want %v", got, want)
	}
}

func TestSessionName(t *testing.T) {
	cases := map[string]string{
		"":                         "session 3",
		"  fix the   import\nmore": "fix the import",
		"adjust the spreadsheet so we can start importing it": "adjust the spreadsheet so we can start…",
	}
	for in, want := range cases {
		if got := sessionName(in, 3); got != want {
			t.Errorf("sessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDailySince(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.Local) }
	cases := map[int]int{9: 6, 10: 9, 14: 13, 15: 13, 13: 12}
	for now, want := range cases {
		if got := dailySince(day(now)); got.Day() != want || got.Hour() != 0 {
			t.Errorf("dailySince(Nov %d) = %s, want Nov %d 00:00", now, got, want)
		}
	}
}

func TestDailyParsing(t *testing.T) {
	text := "do the thing\n\n## Previous sessions on this ticket\nold\n# Linear ticket KAN-1: Import sheets\nurl\n\n## Description\nUsers upload sheets\n## Comments\n- a"
	if got := request(text); got != "do the thing\n" {
		t.Errorf("request = %q", got)
	}
	if got := ticketTopic(text); got != "Import sheets — Users upload sheets" {
		t.Errorf("ticketTopic = %q", got)
	}
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	if got := contentText(raw("<command-name>x</command-name>")); got != "" {
		t.Errorf("contentText kept a tag message: %q", got)
	}
	blocks := []map[string]string{{"type": "text", "text": "hello"}, {"type": "tool_use", "text": "no"}, {"type": "text", "text": "<system>skip"}, {"type": "text", "text": "world"}}
	if got := contentText(raw(blocks)); got != "hello\nworld" {
		t.Errorf("contentText = %q", got)
	}
}

func TestReadEnv(t *testing.T) {
	envFile = filepath.Join(t.TempDir(), ".env")
	os.WriteFile(envFile, []byte("# comment\nexport LINEAR_API_KEY=\"lin_api_x\"\nWORKSPACE_DIR = '~/work'\nbroken line\n"), 0o600)
	env := readEnv()
	if env["LINEAR_API_KEY"] != "lin_api_x" || env["WORKSPACE_DIR"] != "~/work" || len(env) != 2 {
		t.Errorf("readEnv = %v", env)
	}
}

func TestSettings(t *testing.T) {
	cases := []struct {
		tickets []string
		want    []string
	}{
		{nil, []string{"assigned"}},
		{[]string{"bogus"}, []string{"assigned"}},
		{[]string{"subscribed", "assigned"}, []string{"assigned", "subscribed"}},
	}
	for _, c := range cases {
		if got := (Settings{Tickets: c.tickets}).sources(); !slices.Equal(got, c.want) {
			t.Errorf("sources(%v) = %v, want %v", c.tickets, got, c.want)
		}
	}
	if got := (Settings{DailyLanguage: "xx"}).language().code; got != "pt" {
		t.Errorf("unknown language fell back to %s", got)
	}
	s := Settings{HiddenTabs: []string{"kanban", "daily"}}
	if s.tabHidden(tabKanban) || !s.tabHidden(tabDaily) || s.tabHidden(tabSessions) {
		t.Errorf("tabHidden wrong for %v", s.HiddenTabs)
	}
	list := []string{"a", "b"}
	if got := toggle(list, "a"); !slices.Equal(got, []string{"b"}) || !slices.Equal(list, []string{"a", "b"}) {
		t.Errorf("toggle removed wrong or changed the input: %v %v", got, list)
	}
	if got := toggle(list, "c"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("toggle add = %v", got)
	}
}

func TestBuildColumns(t *testing.T) {
	issue := func(key, state, typ string, pos, prio float64) Issue {
		i := Issue{Identifier: key, Priority: prio, UpdatedAt: key}
		i.State = State{Name: state, Type: typ, Position: pos}
		return i
	}
	m := model{
		issues: []Issue{
			issue("A-1", "review", "started", 3, 2),
			issue("A-2", "todo", "unstarted", 1, 0),
			issue("A-3", "doing", "started", 2, 1),
			issue("A-4", "doing", "started", 2, 4),
			issue("A-5", "triage", "triage", 0, 1),
		},
		sessions: map[string][]Session{"A-4": {{ID: "s1"}}},
		live:     map[string]string{"s1": "%1"},
		settings: Settings{HiddenColumns: []string{"triage"}},
	}
	m.buildColumns()
	var got []string
	for _, c := range m.cols {
		got = append(got, c.state.Name)
	}
	if !slices.Equal(got, []string{"todo", "doing", "review"}) {
		t.Fatalf("columns = %v", got)
	}
	if first := m.cols[1].items[0].Identifier; first != "A-4" {
		t.Errorf("a ticket with a running session should come first, got %s", first)
	}
}

func TestForget(t *testing.T) {
	dir := t.TempDir()
	sessionsFile, statusDir = filepath.Join(dir, "sessions.json"), dir
	writeJSON(sessionsFile, map[string][]Session{"A-1": {{ID: "live"}, {ID: "old"}}, "A-2": {{ID: "gone"}}, "A-3": {{ID: "keep"}}})
	for _, id := range []string{"live", "old", "gone", "keep"} {
		os.WriteFile(filepath.Join(dir, id), []byte("waiting"), 0o600)
	}
	m := model{live: map[string]string{"live": "%1"}}
	m.forget([]string{"A-1", "A-2"})
	if m.err != nil {
		t.Fatal(m.err)
	}
	saved, _ := loadSessions()
	if len(saved) != 2 || len(saved["A-1"]) != 1 || saved["A-1"][0].ID != "live" || len(saved["A-3"]) != 1 {
		t.Errorf("sessions after forget = %v", saved)
	}
	for id, want := range map[string]bool{"live": true, "old": false, "gone": false, "keep": true} {
		if _, err := os.Stat(filepath.Join(dir, id)); (err == nil) != want {
			t.Errorf("status file %s exists = %v, want %v", id, err == nil, want)
		}
	}
}

func TestHook(t *testing.T) {
	dir := t.TempDir()
	statusDir, settingsFile = dir, filepath.Join(dir, "settings.json")
	writeJSON(settingsFile, Settings{NotificationsOff: true})
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
		runHook("title", c.event, strings.NewReader(c.input))
		if got := status(); got != c.want {
			t.Errorf("%s %s from %q: status %q, want %q", c.event, c.input, c.before, got, c.want)
		}
	}
	runHook("title", "stop", strings.NewReader(`{"session_id":"../../escape"}`))
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
	if err := printTranscript(file, &out); err != nil || out.String() != "first reply\n\nsecond reply\n\n" {
		t.Errorf("printTranscript = %q, %v", out.String(), err)
	}
}

func TestApplyAnswer(t *testing.T) {
	r := &Review{Threads: []Thread{{ID: "t1", Code: "a\nb", Comment: "old"}}}
	applyAnswer(r, askMsg{threadID: "t1", kind: "comment", prompt: "shorter", answer: "<comment>\nnew text\n</comment>\n<note>\nmade it short\n</note>", sessionID: "s1"})
	if r.Threads[0].Comment != "new text" || r.Sessions["t1:comment"] != "s1" {
		t.Errorf("comment rewrite not applied: %+v %v", r.Threads[0], r.Sessions)
	}
	if got := r.Chats["t1:comment"]; len(got) != 2 || got[1].Text != "made it short" {
		t.Errorf("comment chat = %+v", got)
	}
	applyAnswer(r, askMsg{threadID: "t1", kind: "explanation", prompt: "annotate", answer: "done\n<code>\n```ts\na // here\nb\n```\n</code>"})
	if r.Threads[0].Code != "a // here\nb" || r.Chats["t1:explanation"][1].Text != "done" {
		t.Errorf("code annotation: %q / %+v", r.Threads[0].Code, r.Chats["t1:explanation"])
	}
	applyAnswer(r, askMsg{threadID: "t1", kind: "comment", prompt: "x", err: os.ErrDeadlineExceeded})
	if r.Threads[0].Comment != "new text" || !strings.HasPrefix(r.Chats["t1:comment"][3].Text, "error: ") {
		t.Errorf("a failed call must not touch the comment: %+v", r.Chats["t1:comment"])
	}
}

func TestPRNames(t *testing.T) {
	p := PR{Repo: "acme/shop-web", Number: 1987}
	if k := prKey(p); k != "shop-web#1987" || !isPRKey(k) || isPRKey("KAN-12") {
		t.Errorf("prKey = %s", k)
	}
	if f := filepath.Base(reviewFile("https://github.com/acme/shop-web/pull/1987")); f != "acme-shop-web-1987.json" {
		t.Errorf("reviewFile = %s", f)
	}
	if plural(1, "thread") != "1 thread" || plural(3, "thread") != "3 threads" {
		t.Error("plural")
	}
}
