package main

import (
	"errors"
	"fmt"
	"io"
	"kta/internal/claude"
	"kta/internal/store"
	"kta/internal/tracker"
	"slices"
	"strings"
	"time"
)

func runGroups(w io.Writer) error {
	groups, err := store.LoadGroups()
	if err != nil {
		return err
	}
	sessions, err := store.LoadSessions()
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Fprintln(w, "No groups yet.")
	}
	for _, g := range groups {
		var names []string
		for _, s := range sessions[store.GroupKey(g.ID)] {
			names = append(names, fmt.Sprintf("%q", s.Name))
		}
		fmt.Fprintf(w, "%s  %s  sessions: %s\n", g.ID, strings.Join(g.Tickets, ", "), strings.Join(names, ", "))
	}
	return nil
}

func runGroup(session string, args []string, kind tracker.Kind, fetch func(string) (tracker.Issue, error), w io.Writer) error {
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
	sessions, err := store.LoadSessions()
	if err != nil {
		return err
	}
	groups, err := store.LoadGroups()
	if err != nil {
		return err
	}
	var g *store.Group
	solo := ""
	switch {
	case groupID != "":
		if g = store.GroupByID(groups, groupID); g == nil {
			return fmt.Errorf("no group %s (kta groups lists them)", groupID)
		}
	case session == "":
		return errors.New("KTA_SESSION is not set: run this from a Claude session started by kta, or pass --group <id>")
	default:
		keys := store.KeysOf(sessions, session)
		switch {
		case len(keys) == 0:
			return fmt.Errorf("session %s isn't in kta", session)
		case store.IsGroupKey(keys[0]):
			if g = store.SessionGroup(sessions, groups, session); g == nil {
				return errors.New("this session's group no longer exists: start a new session from the ticket in kta")
			}
		case store.IsTicketKey(keys[0]):
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
		if _, err := store.MutateGroups(func(gs []store.Group) []store.Group { return store.DropTickets(gs, targets) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "Removed. The group now has %s.\n", strings.Join(left, ", "))
		return nil
	case "add":
		if len(targets) == 0 {
			return usage
		}
		var issues []tracker.Issue
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
			if other := store.GroupOf(groups, t); other != nil {
				moved = append(moved, fmt.Sprintf("%s left the group %s (%s)", t, other.ID, other.Title()))
			}
		}
		if len(issues) == 0 {
			fmt.Fprintln(w, "Already in the group: "+strings.Join(current, ", "))
			return nil
		}
		added := make([]string, len(issues))
		for i, issue := range issues {
			added[i] = issue.Key
		}
		var fresh *store.Group
		if g == nil {
			fresh = &store.Group{ID: store.NewUUID(), Tickets: []string{solo}, Hue: store.PickHue(store.UsedHues(groups)), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
			g = fresh
			if other := store.GroupOf(groups, solo); other != nil {
				moved = append(moved, fmt.Sprintf("%s left the group %s (%s)", solo, other.ID, other.Title()))
			}
		}
		id, place := g.ID, added
		_, groups, err = store.MutateState(func(s map[string][]store.Session, gs []store.Group) []store.Group {
			if fresh != nil {
				place = append([]string{solo}, added...)
				for _, x := range s[solo] {
					if x.ID == session {
						s[store.GroupKey(id)] = append(s[store.GroupKey(id)], x)
					}
				}
				if s[solo] = slices.DeleteFunc(s[solo], func(x store.Session) bool { return x.ID == session }); len(s[solo]) == 0 {
					delete(s, solo)
				}
				gs = append(gs, *fresh)
			}
			return store.PlaceTickets(s, gs, id, place)
		})
		if err != nil {
			return err
		}
		if store.GroupByID(groups, id) == nil {
			return fmt.Errorf("the group %s no longer exists", id)
		}
		fmt.Fprintf(w, "Added. The group %s now has %s.\n", id, strings.Join(store.GroupByID(groups, id).Tickets, ", "))
		for _, line := range moved {
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w, "\nThe new tickets:")
		for _, i := range issues {
			fmt.Fprintf(w, "\n%s\n", strings.Join(claude.TicketSection(kind, i), "\n"))
		}
		return nil
	}
	return usage
}
