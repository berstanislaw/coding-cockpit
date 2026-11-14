package main

import (
	"errors"
	"kta/internal/config"
	"kta/internal/store"
	"kta/internal/testenv"
	"kta/internal/tracker"
	"slices"
	"strings"
	"testing"
)

func TestRunGroup(t *testing.T) {
	testenv.UseTemp(t)
	store.WriteJSON(config.GroupsFile, []store.Group{{ID: "other", Tickets: []string{"B-1", "B-2"}}})
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{"A-1": {{ID: "s1", Name: "work"}}, store.GroupKey("other"): {{ID: "o1"}}, "free": {{ID: "f1"}}})
	fetch := func(key string) (tracker.Issue, error) {
		if key == "A-404" {
			return tracker.Issue{}, errors.New("not found")
		}
		return tracker.Issue{Key: key, Title: "title of " + key, Description: "details of " + key}, nil
	}
	run := func(session string, args ...string) (string, error) {
		var out strings.Builder
		err := runGroup(session, args, tracker.All[0], fetch, &out)
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
	groups, _ := store.LoadGroups()
	sessions, _ := store.LoadSessions()
	mine := store.SessionGroup(sessions, groups, "s1")
	if mine == nil || !slices.Equal(mine.Tickets, []string{"A-1", "A-2", "B-1"}) || sessions["A-1"] != nil {
		t.Fatalf("a solo session becomes a group and takes B-1 along: %+v %v", groups, sessions)
	}
	if other := store.GroupByID(groups, "other"); !slices.Equal(other.Tickets, []string{"B-2"}) || other.Hue == mine.Hue {
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
	if other, _ := store.LoadGroups(); !slices.Equal(store.GroupByID(other, "other").Tickets, []string{"B-2", "B-9"}) {
		t.Errorf("a refused remove must not touch the other group: %+v", other)
	}
	if out, err := run("s1", "remove", "A-1", "B-1"); err != nil || !strings.Contains(out, "now has A-2") {
		t.Errorf("remove = %q, %v", out, err)
	}
	if _, err := run("s1", "remove", "A-2"); err == nil {
		t.Error("removing the last ticket must fail")
	}
	store.WriteJSON(config.SessionsFile, map[string][]store.Session{store.GroupKey("gone"): {{ID: "orphan"}}, "B-2": {{ID: "solo"}}})
	if _, err := run("orphan", "add", "A-7"); err == nil {
		t.Error("a session whose group is gone must fail")
	}
	out, err = run("solo", "add", "A-8")
	if err != nil || !strings.Contains(out, "B-2 left the group other") {
		t.Errorf("a solo session on a grouped ticket must say it left: %q, %v", out, err)
	}
	if groups, _ := store.LoadGroups(); slices.ContainsFunc(groups, func(g store.Group) bool { return slices.Contains(g.Tickets, "") }) {
		t.Errorf("no group may hold an empty ticket: %+v", groups)
	}
	var list strings.Builder
	runGroups(&list)
	if !strings.Contains(list.String(), "other  B-9") {
		t.Errorf("kta groups = %q", list.String())
	}
}

func TestMain(m *testing.M) { testenv.Main(m) }
