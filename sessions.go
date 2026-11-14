package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const maxPromptBytes = 120_000

type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

func loadSessions() (map[string][]Session, error) {
	m := map[string][]Session{}
	b, err := os.ReadFile(sessionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func locked(fn func() error) error {
	f, err := os.OpenFile(sessionsFile+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

func loadState() (sessions map[string][]Session, groups []Group, err error) {
	err = locked(func() error {
		var err error
		if sessions, err = loadSessions(); err != nil {
			return err
		}
		groups, err = loadGroups()
		return err
	})
	return sessions, groups, err
}

func mutateState(change func(map[string][]Session, []Group) []Group) (sessions map[string][]Session, groups []Group, err error) {
	err = locked(func() error {
		var err error
		if sessions, err = loadSessions(); err != nil {
			return err
		}
		if groups, err = loadGroups(); err != nil {
			return err
		}
		if groups = change(sessions, groups); groups == nil {
			groups = []Group{}
		}
		if err := writeJSON(groupsFile, groups); err != nil {
			return err
		}
		return writeJSON(sessionsFile, sessions)
	})
	return sessions, groups, err
}

func mutateSessions(change func(map[string][]Session)) (map[string][]Session, error) {
	sessions, _, err := mutateState(func(s map[string][]Session, gs []Group) []Group {
		change(s)
		return gs
	})
	return sessions, err
}

func writeJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}

func prTag(url string) string {
	m := prURL.FindStringSubmatch(url)
	if m == nil {
		return "PR"
	}
	word := m[2]
	if i := strings.LastIndex(word, "-"); i >= 0 && i < len(word)-1 {
		word = word[i+1:]
	}
	return strings.ToUpper(word[:1]) + "#" + m[3]
}

func prLabel(url string) string {
	if m := prURL.FindStringSubmatch(url); m != nil {
		return m[2] + " #" + m[3]
	}
	return url
}

func canonicalPR(url string) string {
	if m := prURL.FindStringSubmatch(url); m != nil {
		return "https://github.com/" + m[1] + "/" + m[2] + "/pull/" + m[3]
	}
	return url
}

func issuePRs(i Issue) []string {
	var out []string
	for _, a := range i.Attachments.Nodes {
		if prURL.MatchString(a.URL) {
			out = append(out, canonicalPR(a.URL))
		}
	}
	return out
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func readStatus() map[string]string {
	status := map[string]string{}
	entries, _ := os.ReadDir(statusDir)
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join(statusDir, e.Name())); err == nil {
			status[e.Name()] = strings.TrimSpace(string(b))
		}
	}
	return status
}

func sessionName(request string, n int) string {
	first, _, _ := strings.Cut(strings.TrimSpace(request), "\n")
	line := strings.Join(strings.Fields(first), " ")
	if line == "" {
		return fmt.Sprintf("session %d", n)
	}
	r := []rune(line)
	if len(r) <= 40 {
		return line
	}
	cut := string(r[:40])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

func previousSessions(list []Session, live map[string]string) []string {
	var lines []string
	for _, s := range list {
		file := filepath.Join(claudeSessionsDir, s.ID+".jsonl")
		if _, err := os.Stat(file); err != nil {
			continue
		}
		running := ""
		if _, ok := live[s.ID]; ok {
			running = ", still running"
		}
		lines = append(lines, fmt.Sprintf(`- "%s" (%.10s%s): %s`, s.Name, s.CreatedAt, running, file))
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{
		"## Previous sessions on this ticket",
		"Other Claude sessions already worked on this ticket. If you need what was done or decided there, read only the replies (the full file is large):",
		"`" + shq(selfExe) + " transcript <file>`",
	}, append(lines, "")...)
}

func buildPrompt(issues []Issue, request string, previous []string) string {
	request = strings.TrimSpace(request)
	switch {
	case request != "":
	case len(issues) > 1:
		request = "Read the tickets below and tell me how you want to proceed."
	default:
		request = "Read the ticket below and tell me how you want to proceed."
	}
	parts := append([]string{request, ""}, previous...)
	if len(issues) > 1 {
		parts = append(parts, fmt.Sprintf("These %d tickets are one piece of work: handle them together.", len(issues)), "")
	}
	for k, i := range issues {
		if k > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, ticketSection(i)...)
	}
	return clipPrompt(strings.Join(parts, "\n"))
}

func clipPrompt(text string) string {
	if len(text) > maxPromptBytes {
		return strings.ToValidUTF8(text[:maxPromptBytes], "")
	}
	return text
}

func ticketSection(i Issue) []string {
	labels := make([]string, len(i.Labels.Nodes))
	for k, l := range i.Labels.Nodes {
		labels[k] = l.Name
	}
	team := "Team"
	if tracker == "jira" {
		team = "Project"
	}
	status := fmt.Sprintf("Status: %s · %s: %s", i.State.Name, team, i.Team.Key)
	if len(labels) > 0 {
		status += " · Labels: " + strings.Join(labels, ", ")
	}
	description := i.Description
	if description == "" {
		description = "(no description)"
	}
	parts := []string{fmt.Sprintf("# %s ticket %s: %s", trackerName(), i.Identifier, i.Title), i.URL, status, "", "## Description", description}
	if len(i.Comments.Nodes) > 0 {
		parts = append(parts, "", "## Comments")
		for _, c := range i.Comments.Nodes {
			name := "integration"
			if c.User != nil {
				name = c.User.Name
			}
			parts = append(parts, fmt.Sprintf("- %s (%.10s): %s", name, c.CreatedAt, c.Body))
		}
	}
	return parts
}

func keysOf(sessions map[string][]Session, id string) []string {
	var keys []string
	for key, list := range sessions {
		if slices.ContainsFunc(list, func(s Session) bool { return s.ID == id }) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func prPrompt(p PR, request string, previous []string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		request = "Read the pull request below and tell me how you want to proceed."
	}
	body, _ := run("", nil, time.Minute, "gh", "pr", "view", p.URL, "--json", "body", "-q", ".body")
	parts := append([]string{request, ""}, previous...)
	parts = append(parts, fmt.Sprintf("# Pull request %s: %s", prKey(p), p.Title), p.URL, "", "## Description", strings.TrimSpace(string(body)))
	if len(p.Threads) > 0 {
		parts = append(parts, "", "## Open review threads")
		for _, t := range p.Threads {
			parts = append(parts, fmt.Sprintf("- %s:%d @%s: %s", t.Path, t.Line, t.Author, t.Body))
		}
	}
	return clipPrompt(strings.Join(parts, "\n"))
}
