package store

import (
	"fmt"
	"kta/internal/config"
	"kta/internal/testenv"
	"slices"
	"sync"
	"testing"
)

func TestPickHue(t *testing.T) {
	var used []int
	for h := 0; h < 360; h += 15 {
		if h != 90 {
			used = append(used, h)
		}
	}
	if got := PickHue(used); got != 90 {
		t.Errorf("pickHue should take the only free hue, got %d", got)
	}
	for range 20 {
		if got := PickHue([]int{MarkHue}); got != 90 {
			t.Errorf("the hue farthest from the mark color is 90, got %d", got)
		}
	}
}

func TestMoveWholeGroup(t *testing.T) {
	testenv.UseTemp(t)
	WriteJSON(config.GroupsFile, []Group{{ID: "old", Tickets: []string{"A-1", "A-2"}}, {ID: "keep", Tickets: []string{"B-1", "B-2"}}})
	WriteJSON(config.SessionsFile, map[string][]Session{GroupKey("old"): {{ID: "s-old"}}, GroupKey("keep"): {{ID: "s-keep"}}})
	sessions, groups, err := MutateState(func(s map[string][]Session, gs []Group) []Group {
		return PlaceTickets(s, append(gs, Group{ID: "new", Tickets: []string{"A-1", "A-2", "B-1"}}), "new", []string{"A-1", "A-2", "B-1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if GroupByID(groups, "old") != nil || len(sessions[GroupKey("new")]) != 1 || sessions[GroupKey("new")][0].ID != "s-old" {
		t.Errorf("an emptied group hands its sessions to where its tickets went: %+v %v", groups, sessions)
	}
	if k := GroupByID(groups, "keep"); k == nil || !slices.Equal(k.Tickets, []string{"B-2"}) || len(sessions[GroupKey("keep")]) != 1 {
		t.Errorf("a group that keeps tickets keeps its sessions: %+v %v", groups, sessions)
	}
	if got := PlaceTickets(sessions, groups, "missing", []string{"B-2"}); GroupByID(got, "keep") == nil || len(GroupByID(got, "keep").Tickets) != 1 {
		t.Error("placing into a missing group must not take tickets from others")
	}
}

func TestConcurrentWrites(t *testing.T) {
	testenv.UseTemp(t)
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			if _, err := MutateGroups(func(gs []Group) []Group { return append(gs, Group{ID: fmt.Sprint(i)}) }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if groups, _ := LoadGroups(); len(groups) != 30 {
		t.Errorf("concurrent writers lost updates: %d of 30 groups", len(groups))
	}
}

func TestMain(m *testing.M) { testenv.Main(m) }
