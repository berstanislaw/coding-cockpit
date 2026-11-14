package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kta-test-")
	if err != nil {
		panic(err)
	}
	rootDir = dir
	useConfDir(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

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
	useTemp(t)
	os.WriteFile(envFile, []byte("# comment\nexport LINEAR_API_KEY=\"lin_api_x\"\nWORKSPACE_DIR = '~/work'\nbroken line\n"), 0o600)
	env := readEnv(envFile)
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
	dir := useTemp(t)
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
	dir := useTemp(t)
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

func TestPickHue(t *testing.T) {
	var used []int
	for h := 0; h < 360; h += 15 {
		if h != 90 {
			used = append(used, h)
		}
	}
	if got := pickHue(used); got != 90 {
		t.Errorf("pickHue should take the only free hue, got %d", got)
	}
	for range 20 {
		if got := pickHue([]int{markHue}); got != 90 {
			t.Errorf("the hue farthest from the mark color is 90, got %d", got)
		}
	}
	if hueColor(0) == hueColor(180) {
		t.Error("hueColor should change with the hue")
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

func TestMarquee(t *testing.T) {
	if got := marquee("KAN-1 · KAN-2", 20, 7); got != "KAN-1 · KAN-2" {
		t.Errorf("text that fits must not move: %q", got)
	}
	s := "KAN-1115 · KAN-1116 · KAN-1117"
	if got := marquee(s, 10, 0); got != "KAN-1115 ·" {
		t.Errorf("frame 0 = %q", got)
	}
	if got := marquee(s, 10, 11); got != "KAN-1116 ·" {
		t.Errorf("frame 11 = %q", got)
	}
	if marquee(s, 10, 3) == marquee(s, 10, 4) {
		t.Error("each frame should move the text")
	}
}

func useTemp(t *testing.T) string {
	useConfDir(t.TempDir())
	os.MkdirAll(statusDir, 0o700)
	return statusDir
}

func todo(key string) Issue {
	i := Issue{Identifier: key, UpdatedAt: key}
	i.State = State{Name: "todo", Type: "unstarted"}
	return i
}

func loaded(t *testing.T, issues ...Issue) model {
	m := model{issues: issues, live: map[string]string{}}
	m.sessions, _ = loadSessions()
	m.groups, _ = loadGroups()
	m.buildColumns()
	return m
}

func TestGroupPrompt(t *testing.T) {
	a, b := Issue{Identifier: "A-1", Title: "Import sheets", Description: "upload"}, Issue{Identifier: "A-2", Title: "Export sheets"}
	p := buildPrompt([]Issue{a, b}, "", nil)
	for _, want := range []string{"Read the tickets below", "These 2 tickets are one piece of work", "# Linear ticket A-1: Import sheets", "# Linear ticket A-2: Export sheets"} {
		if !strings.Contains(p, want) {
			t.Errorf("group prompt misses %q", want)
		}
	}
	if got := ticketTopic(p); got != "Import sheets + Export sheets — upload" {
		t.Errorf("ticketTopic of a group = %q", got)
	}
}

func TestGroups(t *testing.T) {
	dir := useTemp(t)
	g := Group{ID: "g", Tickets: []string{"A-1", "A-2", "A-3"}, Hue: 90}
	writeJSON(groupsFile, []Group{g})
	writeJSON(sessionsFile, map[string][]Session{groupKey("g"): {{ID: "gs", Name: "together", CreatedAt: "2026-11-12T10:00:00Z"}}, "A-2": {{ID: "solo", CreatedAt: "2026-11-12T11:00:00Z"}}})
	os.WriteFile(filepath.Join(dir, "gs"), []byte("waiting"), 0o600)
	m := loaded(t, todo("A-1"), todo("B-9"), todo("A-3"), todo("B-8"), todo("A-2"))
	if got := m.titleOf("gs"); got != "A-1+A-2+A-3 · together" {
		t.Errorf("titleOf = %q", got)
	}
	m.ticket = &Issue{Identifier: "A-2"}
	if got := m.listSessions(); len(got) != 2 || got[0].ID != "solo" || got[1].ID != "gs" {
		t.Errorf("a ticket panel lists its own and its group's sessions, newest first: %+v", got)
	}
	var order []string
	for _, i := range m.cols[0].items {
		order = append(order, i.Identifier)
	}
	if !slices.Equal(order, []string{"A-3", "A-2", "A-1", "B-9", "B-8"}) {
		t.Errorf("group members should sit together: %v", order)
	}
	m.forget([]string{"A-1"})
	if got := m.groupOf("A-2"); got == nil || !slices.Equal(got.Tickets, []string{"A-2", "A-3"}) {
		t.Errorf("a finished ticket leaves the group, the rest stays: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "gs")); err != nil {
		t.Error("the group session must stay for the others")
	}
	m.removeSession("A-2", "gs")
	if g := m.groupOf("A-3"); len(m.groups) != 1 || g == nil || !slices.Equal(g.Tickets, []string{"A-2", "A-3"}) {
		t.Errorf("a group outlives its sessions while it has tickets: %+v", m.groups)
	}
	if saved, _ := loadGroups(); len(saved) != 1 {
		t.Errorf("the group must stay on disk too: %+v", saved)
	}
}

func TestEmptyGroupKeepsLiveSession(t *testing.T) {
	useTemp(t)
	writeJSON(groupsFile, []Group{{ID: "g", Tickets: []string{"A-1"}}})
	writeJSON(sessionsFile, map[string][]Session{groupKey("g"): {{ID: "live"}, {ID: "old"}}})
	m := loaded(t)
	m.live["live"] = "%1"
	m.forget([]string{"A-1"})
	if len(m.groups) != 1 || len(m.sessions[groupKey("g")]) != 1 {
		t.Errorf("an empty group keeps only its running sessions: %+v %v", m.groups, m.sessions)
	}
	delete(m.live, "live")
	m.cleanupGroups()
	if len(m.groups) != 0 || len(m.sessions) != 0 {
		t.Errorf("once they stop, the group goes: %+v %v", m.groups, m.sessions)
	}
}

func TestFold(t *testing.T) {
	useTemp(t)
	writeJSON(groupsFile, []Group{{ID: "g", Tickets: []string{"A-1", "A-2"}}})
	writeJSON(sessionsFile, map[string][]Session{groupKey("g"): {{ID: "gs"}}})
	m := loaded(t, todo("A-1"), todo("B-9"), todo("A-2"))
	ids := func() []string {
		var out []string
		for _, i := range m.cols[0].items {
			out = append(out, i.Identifier)
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []string{"A-2", "A-1", "B-9"}) {
		t.Fatalf("expanded column = %v", got)
	}
	m.row = 1
	m, _ = m.foldKey("z")
	if got := ids(); !slices.Equal(got, []string{"A-2", "B-9"}) || len(m.collapsed["A-2"].issues) != 2 || m.row != 0 {
		t.Errorf("folded column = %v, cursor row %d", got, m.row)
	}
	if saved, _ := loadGroups(); !saved[0].Collapsed {
		t.Error("fold not saved")
	}
	m, _ = m.foldKey("z")
	if len(ids()) != 3 {
		t.Error("z again should unfold")
	}
	m, _ = m.foldKey("Z")
	m, _ = m.foldKey("Z")
	if len(ids()) != 3 || m.groups[0].Collapsed {
		t.Error("Z twice should leave everything unfolded")
	}
	m.search.SetValue("A-1")
	m.buildColumns()
	m, _ = m.foldKey("z")
	m, _ = m.foldKey("z")
	if m.groups[0].Collapsed {
		t.Error("z on a card alone in its column must toggle")
	}
}

func TestRunGroup(t *testing.T) {
	useTemp(t)
	writeJSON(groupsFile, []Group{{ID: "other", Tickets: []string{"B-1", "B-2"}}})
	writeJSON(sessionsFile, map[string][]Session{"A-1": {{ID: "s1", Name: "work"}}, groupKey("other"): {{ID: "o1"}}, "free": {{ID: "f1"}}})
	fetch := func(key string) (Issue, error) {
		if key == "A-404" {
			return Issue{}, errors.New("not found")
		}
		return Issue{Identifier: key, Title: "title of " + key, Description: "details of " + key}, nil
	}
	run := func(session string, args ...string) (string, error) {
		var out strings.Builder
		err := runGroup(session, args, fetch, &out)
		return out.String(), err
	}
	if _, err := run(""); err == nil {
		t.Error("no KTA_SESSION and no --group must fail")
	}
	if _, err := run("f1", "add", "A-2"); err == nil {
		t.Error("a free session can't get a group")
	}
	if _, err := run("s1", "add", "A-404"); err == nil {
		t.Error("a ticket Linear doesn't know must fail")
	}
	out, err := run("s1", "add", "a-2", "B-1")
	if err != nil || !strings.Contains(out, "# Linear ticket A-2: title of A-2") || !strings.Contains(out, "B-1 left the group other") {
		t.Fatalf("add printed %q, %v", out, err)
	}
	groups, _ := loadGroups()
	sessions, _ := loadSessions()
	mine := sessionGroup(sessions, groups, "s1")
	if mine == nil || !slices.Equal(mine.Tickets, []string{"A-1", "A-2", "B-1"}) || sessions["A-1"] != nil {
		t.Fatalf("a solo session becomes a group and takes B-1 along: %+v %v", groups, sessions)
	}
	if other := groupByID(groups, "other"); !slices.Equal(other.Tickets, []string{"B-2"}) || other.Hue == mine.Hue {
		t.Errorf("B-1 should have moved out of the other group: %+v", other)
	}
	if out, _ := run("s1"); out != "A-1\nA-2\nB-1\n" {
		t.Errorf("list = %q", out)
	}
	if _, err := run("", "add", "B-9", "--group", "other"); err != nil {
		t.Errorf("--group should work without a session: %v", err)
	}
	if _, err := run("s1", "remove", "A-9"); err == nil {
		t.Error("removing a ticket outside the group must fail")
	}
	if _, err := run("s1", "remove", "A-1", "B-2"); err == nil {
		t.Error("remove must refuse a ticket from another group")
	}
	if other, _ := loadGroups(); !slices.Equal(groupByID(other, "other").Tickets, []string{"B-2", "B-9"}) {
		t.Errorf("a refused remove must not touch the other group: %+v", other)
	}
	if out, err := run("s1", "remove", "A-1", "B-1"); err != nil || !strings.Contains(out, "now has A-2") {
		t.Errorf("remove = %q, %v", out, err)
	}
	if _, err := run("s1", "remove", "A-2"); err == nil {
		t.Error("removing the last ticket must fail")
	}
	writeJSON(sessionsFile, map[string][]Session{groupKey("gone"): {{ID: "orphan"}}, "B-2": {{ID: "solo"}}})
	if _, err := run("orphan", "add", "A-7"); err == nil {
		t.Error("a session whose group is gone must fail")
	}
	out, err = run("solo", "add", "A-8")
	if err != nil || !strings.Contains(out, "B-2 left the group other") {
		t.Errorf("a solo session on a grouped ticket must say it left: %q, %v", out, err)
	}
	if groups, _ := loadGroups(); slices.ContainsFunc(groups, func(g Group) bool { return slices.Contains(g.Tickets, "") }) {
		t.Errorf("no group may hold an empty ticket: %+v", groups)
	}
	var list strings.Builder
	runGroups(&list)
	if !strings.Contains(list.String(), "other  B-9") {
		t.Errorf("kta groups = %q", list.String())
	}
}

func TestFoldSurvivesFinishedTicket(t *testing.T) {
	useTemp(t)
	writeJSON(groupsFile, []Group{{ID: "g", Tickets: []string{"A-1", "A-2", "A-3"}, Collapsed: true}})
	writeJSON(sessionsFile, map[string][]Session{groupKey("g"): {{ID: "gs"}}})
	m := loaded(t, todo("A-1"), todo("A-2"))
	m.forget([]string{"A-3"})
	m.buildColumns()
	if len(m.cols[0].items) != 1 || len(m.collapsed[m.cols[0].items[0].Identifier].issues) != 2 {
		t.Errorf("the group should stay folded after one ticket finishes: %v", m.cols[0].items)
	}
}

func TestKtaArgs(t *testing.T) {
	m := model{groups: []Group{{ID: "g1", Tickets: []string{"A-1", "A-2"}}}, sessions: map[string][]Session{groupKey("g1"): {{ID: "s1"}}, "A-1": {{ID: "s2"}}}, linearPRs: map[string][]string{"A-2": {"https://github.com/acme/api/pull/7"}}}
	if m.ktaArgs("", nil) != nil {
		t.Error("a session without tickets gets nothing")
	}
	args := m.ktaArgs(m.sessionWork("s1"))
	if len(args) != 2 || args[0] != "--append-system-prompt" {
		t.Fatalf("args = %v", args)
	}
	for _, want := range []string{"works on A-1, A-2 (group g1)", "A-2: https://github.com/acme/api/pull/7", " group add KAN-123", " groups` lists every group"} {
		if !strings.Contains(args[1], want) {
			t.Errorf("kta context misses %q:\n%s", want, args[1])
		}
	}
	group, tickets := m.sessionWork("s2")
	if group != "" || !slices.Equal(tickets, []string{"A-1"}) {
		t.Errorf("sessionWork of a solo session = %q %v", group, tickets)
	}
	if solo := m.ktaArgs(group, tickets); strings.Contains(solo[1], "(group") {
		t.Errorf("a solo session on a grouped ticket isn't in the group:\n%s", solo[1])
	}
}

func TestMoveWholeGroup(t *testing.T) {
	useTemp(t)
	writeJSON(groupsFile, []Group{{ID: "old", Tickets: []string{"A-1", "A-2"}}, {ID: "keep", Tickets: []string{"B-1", "B-2"}}})
	writeJSON(sessionsFile, map[string][]Session{groupKey("old"): {{ID: "s-old"}}, groupKey("keep"): {{ID: "s-keep"}}})
	sessions, groups, err := mutateState(func(s map[string][]Session, gs []Group) []Group {
		return placeTickets(s, append(gs, Group{ID: "new", Tickets: []string{"A-1", "A-2", "B-1"}}), "new", []string{"A-1", "A-2", "B-1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if groupByID(groups, "old") != nil || len(sessions[groupKey("new")]) != 1 || sessions[groupKey("new")][0].ID != "s-old" {
		t.Errorf("an emptied group hands its sessions to where its tickets went: %+v %v", groups, sessions)
	}
	if k := groupByID(groups, "keep"); k == nil || !slices.Equal(k.Tickets, []string{"B-2"}) || len(sessions[groupKey("keep")]) != 1 {
		t.Errorf("a group that keeps tickets keeps its sessions: %+v %v", groups, sessions)
	}
	if got := placeTickets(sessions, groups, "missing", []string{"B-2"}); groupByID(got, "keep") == nil || len(groupByID(got, "keep").Tickets) != 1 {
		t.Error("placing into a missing group must not take tickets from others")
	}
}

func TestCleanupRechecksDisk(t *testing.T) {
	useTemp(t)
	writeJSON(groupsFile, []Group{{ID: "f"}})
	writeJSON(sessionsFile, map[string][]Session{groupKey("f"): {{ID: "new"}}})
	m := loaded(t)
	m.live["new"] = "%1"
	m.sessions = map[string][]Session{}
	m.cleanupGroups()
	if groups, _ := loadGroups(); len(groups) != 1 {
		t.Error("a stale snapshot must not delete a group that has sessions on disk")
	}
}

func TestConcurrentWrites(t *testing.T) {
	useTemp(t)
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			if _, err := mutateGroups(func(gs []Group) []Group { return append(gs, Group{ID: fmt.Sprint(i)}) }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if groups, _ := loadGroups(); len(groups) != 30 {
		t.Errorf("concurrent writers lost updates: %d of 30 groups", len(groups))
	}
}

func TestDiffHunks(t *testing.T) {
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,4 @@ func main() {\n a\n+b\n+++ not a header\n@@ -10 +11 @@\n+c\ndiff --git a/y.go b/y.go\n--- a/y.go\n+++ b/y.go\n@@ -5,2 +6,3 @@\n+d\n"
	got := diffHunks(diff)
	if !slices.Equal(got["x.go"], [][2]int{{1, 4}, {11, 11}}) || !slices.Equal(got["y.go"], [][2]int{{6, 8}}) || len(got) != 2 {
		t.Errorf("diffHunks = %v", got)
	}
}

func TestHookQuoting(t *testing.T) {
	dir := useTemp(t)
	out, exe := filepath.Join(dir, "args"), filepath.Join(dir, "fake-kta")
	os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shq(out)+"\n"), 0o700)
	old := selfExe
	selfExe = exe
	t.Cleanup(func() { selfExe = old })
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
	if b, _ := os.ReadFile(filepath.Join(statusDir, "s1")); string(b) != "waiting" {
		t.Errorf("status = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("the title ran as shell code")
	}
}

func TestReviewGuards(t *testing.T) {
	p := PR{URL: "https://github.com/acme/api/pull/1"}
	m := model{openPR: &p, reviews: map[string]*Review{p.URL: {PRURL: p.URL, Threads: []Thread{{ID: "t1"}}}}, generating: map[string]bool{p.URL: true}, busy: map[string]bool{}}
	if got, _ := m.teamKey("a", ""); got.focus == focusAsk {
		t.Error("no questions while the review is being regenerated")
	}
	m.generating = map[string]bool{}
	m.busy[p.URL+"|t1:comment"] = true
	if got, _ := m.teamKey("r", "r"+p.URL); got.generating[p.URL] {
		t.Error("no regeneration while a question is running")
	}
}

func TestWorkspacePicker(t *testing.T) {
	old := rootDir
	rootDir = t.TempDir()
	t.Cleanup(func() { rootDir = old })
	os.MkdirAll(filepath.Join(rootDir, "workspaces", "jira"), 0o700)
	os.MkdirAll(filepath.Join(rootDir, "workspaces", "Bad Name"), 0o700)
	os.WriteFile(filepath.Join(rootDir, "workspaces", "loose-file"), nil, 0o600)
	os.WriteFile(filepath.Join(rootDir, "workspaces", "jira", "env"), []byte("LINEAR_API_KEY='x'\nWORKSPACE_DIR='~/jira-work'\n"), 0o600)
	list := listWorkspaces()
	if len(list) != 2 || list[0].name != "" || list[1].name != "jira" || list[1].dir != "~/jira-work" {
		t.Fatalf("listWorkspaces = %+v", list)
	}
	names := func(filter string) (out []string) {
		for _, r := range pickRows(list, filter) {
			if r.fresh {
				out = append(out, "+"+r.name)
			} else {
				out = append(out, workspaceLabel(r.name))
			}
		}
		return out
	}
	for filter, want := range map[string][]string{"": {"default", "jira"}, "JI": {"jira", "+ji"}, "default": {"default"}, "gitlab": {"+gitlab"}, "a b": nil} {
		if got := names(filter); !slices.Equal(got, want) {
			t.Errorf("pickRows(%q) = %v, want %v", filter, got, want)
		}
	}
	m := model{pick: textinput.New()}
	m, _ = m.openPicker()
	m, _ = m.pickKey(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.pickKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.pickSel != 1 {
		t.Errorf("the cursor must stop at the last row: %d", m.pickSel)
	}
	m, _ = m.pickKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.pickSel != 0 || m.pick.Value() != "j" {
		t.Errorf("typing filters and resets the cursor: %d %q", m.pickSel, m.pick.Value())
	}
	m, _ = m.pickKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.picking {
		t.Error("esc closes the picker")
	}
	m, _ = m.openPicker()
	m, _ = m.pickKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.picking || m.err != nil {
		t.Errorf("enter on the open workspace just closes: %v", m.err)
	}
}

func TestJiraSite(t *testing.T) {
	for in, want := range map[string]string{"acme": "https://acme.atlassian.net", "acme.atlassian.net": "https://acme.atlassian.net", " https://acme.atlassian.net/jira/software/projects/KAN/boards/1 ": "https://acme.atlassian.net", "http://jira.local:8080/": "http://jira.local:8080", "": ""} {
		if got := jiraSite(in); got != want {
			t.Errorf("jiraSite(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestADF(t *testing.T) {
	doc := `{"type":"doc","content":[
		{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Goal"}]},
		{"type":"paragraph","content":[{"type":"text","text":"Fix "},{"type":"text","text":"this","marks":[{"type":"strong"}]},{"type":"text","text":" now","marks":[{"type":"link","attrs":{"href":"https://x.dev"}}]},{"type":"hardBreak"},{"type":"mention","attrs":{"text":"@Ana"}}]},
		{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"nested"}]}]}]}]}]},
		{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]},
		{"type":"mediaSingle","content":[{"type":"media"}]}
	]}`
	want := "## Goal\n\nFix **this**[ now](https://x.dev)\n@Ana\n\n- one\n  - nested\n\n```go\nx := 1\n```\n\n[attachment]"
	if got := adfMarkdown(json.RawMessage(doc)); got != want {
		t.Errorf("adfMarkdown =\n%s\nwant\n%s", got, want)
	}
	if adfMarkdown(nil) != "" || adfMarkdown(json.RawMessage("null")) != "" {
		t.Error("no description gives an empty string")
	}
}

func TestJiraClient(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "me@acme.com" || p != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/myself":
			fmt.Fprint(w, `{"displayName":"Ana Lima"}`)
		case "/rest/api/3/search/jql":
			var body struct{ JQL, NextPageToken string }
			json.NewDecoder(r.Body).Decode(&body)
			queries = append(queries, body.JQL)
			if body.NextPageToken == "" {
				fmt.Fprint(w, `{"issues":[{"key":"KAN-1","fields":{"summary":"First","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"priority":{"name":"High"},"project":{"key":"KAN"},"labels":["api"]}}],"nextPageToken":"p2"}`)
				return
			}
			fmt.Fprint(w, `{"issues":[{"key":"KAN-2","fields":{"summary":"Second","status":{"name":"To Do","statusCategory":{"key":"new"}},"project":{"key":"KAN"}}}]}`)
		case "/rest/api/3/issue/KAN-1":
			fmt.Fprint(w, `{"key":"KAN-1","fields":{"summary":"First","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"project":{"key":"KAN"},"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Do it"}]}]},"comment":{"comments":[{"author":{"displayName":"Bia"},"created":"2026-11-13T10:00:00.000-0300","body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"ok"}]}]}}]}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["Issue does not exist"]}`)
		}
	}))
	defer srv.Close()
	oldURL, oldEmail, oldToken := jiraURL, jiraEmail, jiraToken
	t.Cleanup(func() { jiraURL, jiraEmail, jiraToken = oldURL, oldEmail, oldToken })
	jiraURL, jiraEmail, jiraToken = srv.URL, "me@acme.com", "tok"
	if name, err := jiraMyself(); err != nil || name != "Ana Lima" {
		t.Fatalf("jiraMyself = %q, %v", name, err)
	}
	issues, err := jiraBoard([]string{"assigned", "created"})
	if err != nil || len(issues) != 2 {
		t.Fatalf("jiraBoard = %+v, %v", issues, err)
	}
	if queries[0] != "(assignee = currentUser() OR reporter = currentUser()) ORDER BY updated DESC" {
		t.Errorf("jql = %q", queries[0])
	}
	first := issues[0]
	if first.State.Type != "started" || first.State.Name != "In Progress" || first.Priority != 2 || first.URL != srv.URL+"/browse/KAN-1" || first.Team.Key != "KAN" || first.Labels.Nodes[0].Name != "api" {
		t.Errorf("mapped issue = %+v", first)
	}
	if issues[1].State.Type != "unstarted" || issues[1].Priority != 0 {
		t.Errorf("second issue = %+v", issues[1])
	}
	detail, err := jiraIssueDetail("KAN-1")
	if err != nil || detail.Description != "Do it" || len(detail.Comments.Nodes) != 1 || detail.Comments.Nodes[0].User.Name != "Bia" || detail.Comments.Nodes[0].Body != "ok" {
		t.Errorf("detail = %+v, %v", detail, err)
	}
	if _, err := jiraIssueDetail("KAN-404"); err == nil || !strings.Contains(err.Error(), "Issue does not exist") {
		t.Errorf("missing issue error = %v", err)
	}
	jiraToken = "wrong"
	if _, err := jiraMyself(); err == nil || !strings.Contains(err.Error(), "didn't accept") {
		t.Errorf("bad token error = %v", err)
	}
}

func TestWizard(t *testing.T) {
	useTemp(t)
	folder := t.TempDir()
	press := func(w wizard, keys ...string) wizard {
		for _, k := range keys {
			var msg tea.KeyMsg
			switch k {
			case "enter":
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			case "down":
				msg = tea.KeyMsg{Type: tea.KeyDown}
			case "tab":
				msg = tea.KeyMsg{Type: tea.KeyTab}
			case "esc":
				msg = tea.KeyMsg{Type: tea.KeyEsc}
			default:
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
			}
			next, _ := w.Update(msg)
			w = next.(wizard)
		}
		return w
	}
	w := press(newWizard(nil), "down", "enter")
	if w.step != stepAccount || len(w.inputs) != 3 {
		t.Fatalf("choosing Jira opens its three fields: step %d, %d fields", w.step, len(w.inputs))
	}
	w = press(w, "acme", "enter")
	if w.checking || w.err != "Email is missing" || w.field != 1 {
		t.Errorf("enter with an empty field jumps to it: %q, field %d", w.err, w.field)
	}
	w = press(w, "me@acme.com", "tab", "tok", "enter")
	if !w.checking {
		t.Fatalf("all fields filled starts the check: %+v", w.err)
	}
	next, _ := w.Update(accountMsg{err: errors.New("Jira didn't accept the email and API token")})
	if w = next.(wizard); w.step != stepAccount || w.err == "" {
		t.Error("a failed check stays on the account step with the error")
	}
	w = press(w, "esc", "enter")
	if w.inputs[0].Value() != "acme" {
		t.Error("going back and forth keeps what was typed")
	}
	next, _ = w.Update(accountMsg{name: "Ana Lima"})
	w = next.(wizard)
	w.folder.SetValue("/no/such/folder")
	if w = press(w, "enter"); w.step != stepFolder || w.err == "" {
		t.Error("a folder that doesn't exist is refused")
	}
	w.folder.SetValue(folder)
	w = press(w, "enter", "enter")
	if !w.saved {
		t.Fatalf("enter on the summary saves: %q", w.err)
	}
	env := readEnv(envFile)
	if env["TRACKER"] != "jira" || env["JIRA_URL"] != "https://acme.atlassian.net" || env["JIRA_EMAIL"] != "me@acme.com" || env["JIRA_API_TOKEN"] != "tok" || env["WORKSPACE_DIR"] != folder {
		t.Errorf("saved env = %v", env)
	}
	if info, _ := os.Stat(envFile); info.Mode().Perm() != 0o600 {
		t.Errorf("env mode = %v", info.Mode().Perm())
	}
	if err := loadConfig(); err != nil || tracker != "jira" || jiraURL != "https://acme.atlassian.net" {
		t.Errorf("loadConfig after the wizard: %v, tracker %s", err, tracker)
	}
	edit := press(newWizard(env), "enter")
	if edit.choice != 1 || edit.inputs[0].Value() != "acme.atlassian.net" || edit.inputs[1].Value() != "me@acme.com" || edit.inputs[2].Value() != "" {
		t.Errorf("editing prefills everything but the token: %q %q %q", edit.inputs[0].Value(), edit.inputs[1].Value(), edit.inputs[2].Value())
	}
	if edit.values()["JIRA_API_TOKEN"] != "tok" {
		t.Error("an empty token keeps the current one")
	}
	edit = press(newWizard(env), "esc")
	edit.choice = 0
	edit = press(edit, "enter")
	edit.inputs[0].SetValue("lin_api_new")
	if got := edit.env(); got["TRACKER"] != "linear" || got["LINEAR_API_KEY"] != "lin_api_new" || got["JIRA_API_TOKEN"] != "" {
		t.Errorf("switching to Linear drops the Jira credentials: %v", got)
	}
	tracker = "linear"
}

func TestEveryStatus(t *testing.T) {
	useTemp(t)
	writeJSON(sessionsFile, map[string][]Session{"A-2": {{ID: "old"}, {ID: "running"}}, "A-1": {{ID: "keep"}}})
	done := Issue{Identifier: "A-2", State: State{Name: "Done", Type: "completed"}}
	doing := Issue{Identifier: "A-1", State: State{Name: "Doing", Type: "started", Position: 2}}
	m := loaded(t, doing, done)
	m.live["running"] = "%1"
	m.ta = textarea.New()
	m.states = []State{{Name: "Canceled", Type: "canceled"}, {Name: "Done", Type: "completed"}, {Name: "Doing", Type: "started", Position: 2}, {Name: "Todo", Type: "unstarted"}, {Name: "Backlog", Type: "backlog"}, {Name: "Odd", Type: "duplicate"}}
	m.settings.HiddenColumns = []string{"Backlog"}
	m.buildColumns()
	var got []string
	for _, c := range m.cols {
		got = append(got, fmt.Sprintf("%s %d", c.state.Name, len(c.items)))
	}
	if !slices.Equal(got, []string{"Todo 0", "Doing 1", "Done 1", "Canceled 0"}) {
		t.Errorf("every known status is a column, even empty, unless hidden: %v", got)
	}
	next, _ := m.Update(boardMsg{issues: []Issue{doing, done}})
	m = next.(model)
	if s := m.sessions["A-2"]; len(s) != 1 || s[0].ID != "running" || len(m.sessions["A-1"]) != 1 {
		t.Errorf("a finished ticket on the board drops its stopped sessions only: %v", m.sessions)
	}
	if !slices.Equal(m.stateTeams, []string{""}) {
		t.Errorf("the board asks for the statuses of its teams: %v", m.stateTeams)
	}
}
