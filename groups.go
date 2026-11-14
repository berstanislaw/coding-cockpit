package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type Group struct {
	ID        string   `json:"id"`
	Tickets   []string `json:"tickets"`
	Hue       int      `json:"hue"`
	Collapsed bool     `json:"collapsed,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

const groupPrefix = "group:"

func groupKey(id string) string { return groupPrefix + id }

func isGroupKey(key string) bool { return strings.HasPrefix(key, groupPrefix) }

func isTicketKey(key string) bool { return key != freeKey && !isPRKey(key) && !isGroupKey(key) }

func (g Group) color() lipgloss.Color { return hueColor(float64(g.Hue)) }

func (g Group) title() string {
	if len(g.Tickets) == 0 {
		return "empty group"
	}
	return strings.Join(g.Tickets, "+")
}

func loadGroups() ([]Group, error) {
	var groups []Group
	b, err := os.ReadFile(groupsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &groups)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", groupsFile, err)
	}
	return groups, nil
}

func mutateGroups(change func([]Group) []Group) ([]Group, error) {
	_, groups, err := mutateState(func(_ map[string][]Session, gs []Group) []Group { return change(gs) })
	return groups, err
}

func groupOf(groups []Group, ticket string) *Group {
	for i := range groups {
		if slices.Contains(groups[i].Tickets, ticket) {
			return &groups[i]
		}
	}
	return nil
}

func groupByID(groups []Group, id string) *Group {
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i]
		}
	}
	return nil
}

func usedHues(groups []Group) []int {
	hues := []int{markHue}
	for _, g := range groups {
		hues = append(hues, g.Hue)
	}
	return hues
}

func placeTickets(sessions map[string][]Session, groups []Group, id string, tickets []string) []Group {
	if groupByID(groups, id) == nil {
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
			sessions[groupKey(id)] = append(sessions[groupKey(id)], sessions[groupKey(g.ID)]...)
			delete(sessions, groupKey(g.ID))
		}
	}
	return slices.DeleteFunc(groups, func(g Group) bool { return slices.Contains(emptied, g.ID) })
}

func dropTickets(groups []Group, tickets []string) []Group {
	for i := range groups {
		groups[i].Tickets = slices.DeleteFunc(groups[i].Tickets, func(t string) bool { return slices.Contains(tickets, t) })
	}
	return groups
}

func sessionGroup(sessions map[string][]Session, groups []Group, id string) *Group {
	for _, key := range keysOf(sessions, id) {
		if isGroupKey(key) {
			return groupByID(groups, strings.TrimPrefix(key, groupPrefix))
		}
	}
	return nil
}

func staleGroups(sessions map[string][]Session, groups []Group, live map[string]string) (dropSessions, dropGroups []string) {
	for _, g := range groups {
		if len(g.Tickets) > 0 {
			continue
		}
		running := false
		for _, x := range sessions[groupKey(g.ID)] {
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

func runGroups(w io.Writer) error {
	groups, err := loadGroups()
	if err != nil {
		return err
	}
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Fprintln(w, "No groups yet.")
	}
	for _, g := range groups {
		var names []string
		for _, s := range sessions[groupKey(g.ID)] {
			names = append(names, fmt.Sprintf("%q", s.Name))
		}
		fmt.Fprintf(w, "%s  %s  sessions: %s\n", g.ID, strings.Join(g.Tickets, ", "), strings.Join(names, ", "))
	}
	return nil
}

func runGroup(session string, args []string, fetch func(string) (Issue, error), w io.Writer) error {
	usage := errors.New("usage: kta group | kta group add KAN-123 … | kta group remove KAN-123 …  (add --group <id> to change another group)")
	groupID, cmd := "", "list"
	var targets []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--group" && i+1 < len(args):
			groupID = args[i+1]
			i++
		case cmd == "list" && len(targets) == 0 && (a == "list" || a == "add" || a == "remove"):
			cmd = a
		default:
			targets = append(targets, strings.ToUpper(strings.TrimSpace(a)))
		}
	}
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	groups, err := loadGroups()
	if err != nil {
		return err
	}
	var g *Group
	solo := ""
	switch {
	case groupID != "":
		if g = groupByID(groups, groupID); g == nil {
			return fmt.Errorf("no group %s (kta groups lists them)", groupID)
		}
	case session == "":
		return errors.New("KTA_SESSION is not set: run this from a Claude session started by kta, or pass --group <id>")
	default:
		keys := keysOf(sessions, session)
		switch {
		case len(keys) == 0:
			return fmt.Errorf("session %s isn't in kta", session)
		case isGroupKey(keys[0]):
			if g = sessionGroup(sessions, groups, session); g == nil {
				return errors.New("this session's group no longer exists: start a new session from the ticket in kta")
			}
		case isTicketKey(keys[0]):
			solo = keys[0]
		default:
			return errors.New("only sessions started from a ticket can have a group")
		}
	}
	current := []string{solo}
	if g != nil {
		current = g.Tickets
	}
	switch cmd {
	case "list":
		fmt.Fprintln(w, strings.Join(current, "\n"))
		return nil
	case "remove":
		if len(targets) == 0 {
			return usage
		}
		outside := slices.DeleteFunc(slices.Clone(targets), func(t string) bool { return slices.Contains(current, t) })
		left := slices.DeleteFunc(slices.Clone(current), func(t string) bool { return slices.Contains(targets, t) })
		switch {
		case len(outside) > 0:
			return fmt.Errorf("%s isn't in this group (%s)", strings.Join(outside, ", "), strings.Join(current, ", "))
		case len(left) == 0:
			return errors.New("a group needs at least one ticket: it goes away on its own once all its tickets are finished")
		}
		if _, err := mutateGroups(func(gs []Group) []Group { return dropTickets(gs, targets) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "Removed. The group now has %s.\n", strings.Join(left, ", "))
		return nil
	case "add":
		if len(targets) == 0 {
			return usage
		}
		var issues []Issue
		var moved []string
		for _, t := range targets {
			if slices.Contains(current, t) {
				continue
			}
			issue, err := fetch(t)
			if err != nil {
				return fmt.Errorf("%s: %w", t, err)
			}
			issues = append(issues, issue)
			if other := groupOf(groups, t); other != nil {
				moved = append(moved, fmt.Sprintf("%s left the group %s (%s)", t, other.ID, other.title()))
			}
		}
		if len(issues) == 0 {
			fmt.Fprintln(w, "Already in the group: "+strings.Join(current, ", "))
			return nil
		}
		added := make([]string, len(issues))
		for i, issue := range issues {
			added[i] = issue.Identifier
		}
		var fresh *Group
		if g == nil {
			fresh = &Group{ID: newUUID(), Tickets: []string{solo}, Hue: pickHue(usedHues(groups)), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
			g = fresh
			if other := groupOf(groups, solo); other != nil {
				moved = append(moved, fmt.Sprintf("%s left the group %s (%s)", solo, other.ID, other.title()))
			}
		}
		id, place := g.ID, added
		_, groups, err = mutateState(func(s map[string][]Session, gs []Group) []Group {
			if fresh != nil {
				place = append([]string{solo}, added...)
				for _, x := range s[solo] {
					if x.ID == session {
						s[groupKey(id)] = append(s[groupKey(id)], x)
					}
				}
				if s[solo] = slices.DeleteFunc(s[solo], func(x Session) bool { return x.ID == session }); len(s[solo]) == 0 {
					delete(s, solo)
				}
				gs = append(gs, *fresh)
			}
			return placeTickets(s, gs, id, place)
		})
		if err != nil {
			return err
		}
		if groupByID(groups, id) == nil {
			return fmt.Errorf("the group %s no longer exists", id)
		}
		fmt.Fprintf(w, "Added. The group %s now has %s.\n", id, strings.Join(groupByID(groups, id).Tickets, ", "))
		for _, line := range moved {
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w, "\nThe new tickets:")
		for _, i := range issues {
			fmt.Fprintf(w, "\n%s\n", strings.Join(ticketSection(i), "\n"))
		}
		return nil
	}
	return usage
}
