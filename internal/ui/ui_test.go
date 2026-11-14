package ui

import (
	"errors"
	"fmt"
	"kta/internal/config"
	"kta/internal/github"
	"kta/internal/review"
	"kta/internal/store"
	"kta/internal/testenv"
	"kta/internal/tracker"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kta-test-")
	if err != nil {
		panic(err)
	}
	config.RootDir = dir
	config.UseDir(dir)
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
		if got := github.PRTag(c.url); got != c.tag {
			t.Errorf("prTag(%s) = %s, want %s", c.url, got, c.tag)
		}
		if got := github.PRLabel(c.url); got != c.label {
			t.Errorf("prLabel(%s) = %s, want %s", c.url, got, c.label)
		}
	}
	var i tracker.Issue
	for _, u := range []string{"https://github.com/acme/api/pull/1/files", "https://linear.app/x", "https://github.com/acme/web/pull/2"} {
		i.Attachments.Nodes = append(i.Attachments.Nodes, struct{ URL string }{u})
	}
	want := []string{"https://github.com/acme/api/pull/1", "https://github.com/acme/web/pull/2"}
	if got := issuePRs(i); !slices.Equal(got, want) {
		t.Errorf("issuePRs = %v, want %v", got, want)
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
		if got := (store.Settings{Tickets: c.tickets}).Sources(); !slices.Equal(got, c.want) {
			t.Errorf("sources(%v) = %v, want %v", c.tickets, got, c.want)
		}
	}
	if got := (store.Settings{DailyLanguage: "xx"}).Language().Code; got != "pt" {
		t.Errorf("unknown language fell back to %s", got)
	}
	s := store.Settings{HiddenTabs: []string{"kanban", "daily"}}
	if tabHidden(s, tabKanban) || !tabHidden(s, tabDaily) || tabHidden(s, tabSessions) {
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
	issue := func(key, state, typ string, pos, prio float64) tracker.Issue {
		i := tracker.Issue{Identifier: key, Priority: prio, UpdatedAt: key}
		i.State = tracker.State{Name: state, Type: typ, Position: pos}
		return i
	}
	m := model{
		issues: []tracker.Issue{
			issue("A-1", "review", "started", 3, 2),
			issue("A-2", "todo", "unstarted", 1, 0),
			issue("A-3", "doing", "started", 2, 1),
			issue("A-4", "doing", "started", 2, 4),
			issue("A-5", "triage", "triage", 0, 1),
		},
		sessions: map[string][]store.Session{"A-4": {{ID: "s1"}}},
		live:     map[string]string{"s1": "%1"},
		settings: store.Settings{HiddenColumns: []string{"triage"}},
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
	dir := testenv.UseTemp(t)
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{"A-1": {{ID: "live"}, {ID: "old"}}, "A-2": {{ID: "gone"}}, "A-3": {{ID: "keep"}}})
	for _, id := range []string{"live", "old", "gone", "keep"} {
		os.WriteFile(filepath.Join(dir, id), []byte("waiting"), 0o600)
	}
	m := model{live: map[string]string{"live": "%1"}}
	m.forget([]string{"A-1", "A-2"})
	if m.err != nil {
		t.Fatal(m.err)
	}
	saved, _ := store.LoadSessions()
	if len(saved) != 2 || len(saved["A-1"]) != 1 || saved["A-1"][0].ID != "live" || len(saved["A-3"]) != 1 {
		t.Errorf("sessions after forget = %v", saved)
	}
	for id, want := range map[string]bool{"live": true, "old": false, "gone": false, "keep": true} {
		if _, err := os.Stat(filepath.Join(dir, id)); (err == nil) != want {
			t.Errorf("status file %s exists = %v, want %v", id, err == nil, want)
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

func todo(key string) tracker.Issue {
	i := tracker.Issue{Identifier: key, UpdatedAt: key}
	i.State = tracker.State{Name: "todo", Type: "unstarted"}
	return i
}

func loaded(t *testing.T, issues ...tracker.Issue) model {
	m := model{issues: issues, live: map[string]string{}}
	m.sessions, _ = store.LoadSessions()
	m.groups, _ = store.LoadGroups()
	m.buildColumns()
	return m
}

func TestGroups(t *testing.T) {
	dir := testenv.UseTemp(t)
	g := store.Group{ID: "g", Tickets: []string{"A-1", "A-2", "A-3"}, Hue: 90}
	store.WriteJSON(config.GroupsFile, []store.Group{g})
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{store.GroupKey("g"): {{ID: "gs", Name: "together", CreatedAt: "2026-11-12T10:00:00Z"}}, "A-2": {{ID: "solo", CreatedAt: "2026-11-12T11:00:00Z"}}})
	os.WriteFile(filepath.Join(dir, "gs"), []byte("waiting"), 0o600)
	m := loaded(t, todo("A-1"), todo("B-9"), todo("A-3"), todo("B-8"), todo("A-2"))
	if got := m.titleOf("gs"); got != "A-1+A-2+A-3 · together" {
		t.Errorf("titleOf = %q", got)
	}
	m.ticket = &tracker.Issue{Identifier: "A-2"}
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
	if saved, _ := store.LoadGroups(); len(saved) != 1 {
		t.Errorf("the group must stay on disk too: %+v", saved)
	}
}

func TestEmptyGroupKeepsLiveSession(t *testing.T) {
	testenv.UseTemp(t)
	store.WriteJSON(config.GroupsFile, []store.Group{{ID: "g", Tickets: []string{"A-1"}}})
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{store.GroupKey("g"): {{ID: "live"}, {ID: "old"}}})
	m := loaded(t)
	m.live["live"] = "%1"
	m.forget([]string{"A-1"})
	if len(m.groups) != 1 || len(m.sessions[store.GroupKey("g")]) != 1 {
		t.Errorf("an empty group keeps only its running sessions: %+v %v", m.groups, m.sessions)
	}
	delete(m.live, "live")
	m.cleanupGroups()
	if len(m.groups) != 0 || len(m.sessions) != 0 {
		t.Errorf("once they stop, the group goes: %+v %v", m.groups, m.sessions)
	}
}

func TestFold(t *testing.T) {
	testenv.UseTemp(t)
	store.WriteJSON(config.GroupsFile, []store.Group{{ID: "g", Tickets: []string{"A-1", "A-2"}}})
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{store.GroupKey("g"): {{ID: "gs"}}})
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
	if saved, _ := store.LoadGroups(); !saved[0].Collapsed {
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

func TestFoldSurvivesFinishedTicket(t *testing.T) {
	testenv.UseTemp(t)
	store.WriteJSON(config.GroupsFile, []store.Group{{ID: "g", Tickets: []string{"A-1", "A-2", "A-3"}, Collapsed: true}})
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{store.GroupKey("g"): {{ID: "gs"}}})
	m := loaded(t, todo("A-1"), todo("A-2"))
	m.forget([]string{"A-3"})
	m.buildColumns()
	if len(m.cols[0].items) != 1 || len(m.collapsed[m.cols[0].items[0].Identifier].issues) != 2 {
		t.Errorf("the group should stay folded after one ticket finishes: %v", m.cols[0].items)
	}
}

func TestKtaArgs(t *testing.T) {
	m := model{groups: []store.Group{{ID: "g1", Tickets: []string{"A-1", "A-2"}}}, sessions: map[string][]store.Session{store.GroupKey("g1"): {{ID: "s1"}}, "A-1": {{ID: "s2"}}}, linearPRs: map[string][]string{"A-2": {"https://github.com/acme/api/pull/7"}}}
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

func TestCleanupRechecksDisk(t *testing.T) {
	testenv.UseTemp(t)
	store.WriteJSON(config.GroupsFile, []store.Group{{ID: "f"}})
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{store.GroupKey("f"): {{ID: "new"}}})
	m := loaded(t)
	m.live["new"] = "%1"
	m.sessions = map[string][]store.Session{}
	m.cleanupGroups()
	if groups, _ := store.LoadGroups(); len(groups) != 1 {
		t.Error("a stale snapshot must not delete a group that has sessions on disk")
	}
}

func TestReviewGuards(t *testing.T) {
	p := github.PR{URL: "https://github.com/acme/api/pull/1"}
	m := model{openPR: &p, reviews: map[string]*review.Review{p.URL: {PRURL: p.URL, Threads: []review.Thread{{ID: "t1"}}}}, generating: map[string]bool{p.URL: true}, busy: map[string]bool{}}
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
	old := config.RootDir
	config.RootDir = t.TempDir()
	t.Cleanup(func() { config.RootDir = old })
	os.MkdirAll(filepath.Join(config.RootDir, "workspaces", "jira"), 0o700)
	os.MkdirAll(filepath.Join(config.RootDir, "workspaces", "Bad Name"), 0o700)
	os.WriteFile(filepath.Join(config.RootDir, "workspaces", "loose-file"), nil, 0o600)
	os.WriteFile(filepath.Join(config.RootDir, "workspaces", "jira", "env"), []byte("LINEAR_API_KEY='x'\nWORKSPACE_DIR='~/jira-work'\n"), 0o600)
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

func TestWizard(t *testing.T) {
	testenv.UseTemp(t)
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
	env := config.ReadEnv(config.EnvFile)
	if env["TRACKER"] != "jira" || env["JIRA_URL"] != "https://acme.atlassian.net" || env["JIRA_EMAIL"] != "me@acme.com" || env["JIRA_API_TOKEN"] != "tok" || env["WORKSPACE_DIR"] != folder {
		t.Errorf("saved env = %v", env)
	}
	if info, _ := os.Stat(config.EnvFile); info.Mode().Perm() != 0o600 {
		t.Errorf("env mode = %v", info.Mode().Perm())
	}
	if err := config.Load(); err != nil || tracker.Kind != "jira" || tracker.JiraURL != "https://acme.atlassian.net" {
		t.Errorf("loadConfig after the wizard: %v, tracker %s", err, tracker.Kind)
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
	tracker.Kind = "linear"
}

func TestEveryStatus(t *testing.T) {
	testenv.UseTemp(t)
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{"A-2": {{ID: "old"}, {ID: "running"}}, "A-1": {{ID: "keep"}}})
	done := tracker.Issue{Identifier: "A-2", State: tracker.State{Name: "Done", Type: "completed"}}
	doing := tracker.Issue{Identifier: "A-1", State: tracker.State{Name: "Doing", Type: "started", Position: 2}}
	m := loaded(t, doing, done)
	m.live["running"] = "%1"
	m.ta = textarea.New()
	m.states = []tracker.State{{Name: "Canceled", Type: "canceled"}, {Name: "Done", Type: "completed"}, {Name: "Doing", Type: "started", Position: 2}, {Name: "Todo", Type: "unstarted"}, {Name: "Backlog", Type: "backlog"}, {Name: "Odd", Type: "duplicate"}}
	m.settings.HiddenColumns = []string{"Backlog"}
	m.buildColumns()
	var got []string
	for _, c := range m.cols {
		got = append(got, fmt.Sprintf("%s %d", c.state.Name, len(c.items)))
	}
	if !slices.Equal(got, []string{"Todo 0", "Doing 1", "Done 1", "Canceled 0"}) {
		t.Errorf("every known status is a column, even empty, unless hidden: %v", got)
	}
	next, _ := m.Update(boardMsg{issues: []tracker.Issue{doing, done}})
	m = next.(model)
	if s := m.sessions["A-2"]; len(s) != 1 || s[0].ID != "running" || len(m.sessions["A-1"]) != 1 {
		t.Errorf("a finished ticket on the board drops its stopped sessions only: %v", m.sessions)
	}
	if !slices.Equal(m.stateTeams, []string{""}) {
		t.Errorf("the board asks for the statuses of its teams: %v", m.stateTeams)
	}
}

func TestHelpers(t *testing.T) {
	if plural(1, "thread") != "1 thread" || plural(3, "thread") != "3 threads" {
		t.Error("plural")
	}
	if hueColor(0) == hueColor(180) {
		t.Error("hueColor should change with the hue")
	}
}
