package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"kta/internal/config"
	"kta/internal/github"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
)

func GroupKey(id string) string { return GroupPrefix + id }

func IsGroupKey(key string) bool { return strings.HasPrefix(key, GroupPrefix) }

func IsTicketKey(key string) bool { return key != FreeKey && !github.IsPRKey(key) && !IsGroupKey(key) }

func (g Group) Title() string {
	if len(g.Tickets) == 0 {
		return "empty group"
	}
	return strings.Join(g.Tickets, "+")
}

func LoadGroups() ([]Group, error) {
	var groups []Group
	b, err := os.ReadFile(config.GroupsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &groups)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.GroupsFile, err)
	}
	return groups, nil
}

func MutateGroups(change func([]Group) []Group) ([]Group, error) {
	_, groups, err := MutateState(func(_ map[string][]Session, gs []Group) []Group { return change(gs) })
	return groups, err
}

func GroupOf(groups []Group, ticket string) *Group {
	for i := range groups {
		if slices.Contains(groups[i].Tickets, ticket) {
			return &groups[i]
		}
	}
	return nil
}

func GroupByID(groups []Group, id string) *Group {
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i]
		}
	}
	return nil
}

func UsedHues(groups []Group) []int {
	hues := []int{MarkHue}
	for _, g := range groups {
		hues = append(hues, g.Hue)
	}
	return hues
}

func PlaceTickets(sessions map[string][]Session, groups []Group, id string, tickets []string) []Group {
	if GroupByID(groups, id) == nil {
		return groups
	}
	var emptied []string
	for i := range groups {
		g := &groups[i]
		if g.ID == id {
			for _, t := range tickets {
				if !slices.Contains(g.Tickets, t) {
					g.Tickets = append(g.Tickets, t)
				}
			}
			slices.Sort(g.Tickets)
			continue
		}
		had := len(g.Tickets)
		if g.Tickets = slices.DeleteFunc(g.Tickets, func(t string) bool { return slices.Contains(tickets, t) }); had > 0 && len(g.Tickets) == 0 {
			emptied = append(emptied, g.ID)
			sessions[GroupKey(id)] = append(sessions[GroupKey(id)], sessions[GroupKey(g.ID)]...)
			delete(sessions, GroupKey(g.ID))
		}
	}
	return slices.DeleteFunc(groups, func(g Group) bool { return slices.Contains(emptied, g.ID) })
}

func DropTickets(groups []Group, tickets []string) []Group {
	for i := range groups {
		groups[i].Tickets = slices.DeleteFunc(groups[i].Tickets, func(t string) bool { return slices.Contains(tickets, t) })
	}
	return groups
}

func SessionGroup(sessions map[string][]Session, groups []Group, id string) *Group {
	for _, key := range KeysOf(sessions, id) {
		if IsGroupKey(key) {
			return GroupByID(groups, strings.TrimPrefix(key, GroupPrefix))
		}
	}
	return nil
}

func StaleGroups(sessions map[string][]Session, groups []Group, live map[string]string) (dropSessions, dropGroups []string) {
	for _, g := range groups {
		if len(g.Tickets) > 0 {
			continue
		}
		running := false
		for _, x := range sessions[GroupKey(g.ID)] {
			if _, ok := live[x.ID]; ok {
				running = true
			} else {
				dropSessions = append(dropSessions, x.ID)
			}
		}
		if !running {
			dropGroups = append(dropGroups, g.ID)
		}
	}
	return dropSessions, dropGroups
}

func PickHue(used []int) int {
	best, bestDist := []int{}, -1
	for h := 0; h < 360; h += 15 {
		dist := 360
		for _, u := range used {
			d := (h - u + 360) % 360
			dist = min(dist, d, 360-d)
		}
		switch {
		case dist > bestDist:
			best, bestDist = []int{h}, dist
		case dist == bestDist:
			best = append(best, h)
		}
	}
	return best[rand.IntN(len(best))]
}

type Group struct {
	ID        string   `json:"id"`
	Tickets   []string `json:"tickets"`
	Hue       int      `json:"hue"`
	Collapsed bool     `json:"collapsed,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

const GroupPrefix = "group:"

var MarkHue = 270
