package claude

import (
	"fmt"
	"kta/internal/config"
	"kta/internal/github"
	"kta/internal/shell"
	"kta/internal/store"
	"kta/internal/tracker"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func SessionName(request string, n int) string {
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

func PreviousSessions(list []store.Session, live map[string]string) []string {
	var lines []string
	for _, s := range list {
		file := filepath.Join(config.ClaudeSessionsDir, s.ID+".jsonl")
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
		"`" + shell.Quote(config.SelfExe) + " transcript <file>`",
	}, append(lines, "")...)
}

func BuildPrompt(issues []tracker.Issue, request string, previous []string) string {
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
		parts = append(parts, TicketSection(i)...)
	}
	return clipPrompt(strings.Join(parts, "\n"))
}

func clipPrompt(text string) string {
	if len(text) > MaxPromptBytes {
		return strings.ToValidUTF8(text[:MaxPromptBytes], "")
	}
	return text
}

func TicketSection(i tracker.Issue) []string {
	labels := make([]string, len(i.Labels.Nodes))
	for k, l := range i.Labels.Nodes {
		labels[k] = l.Name
	}
	team := "Team"
	if tracker.Kind == "jira" {
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
	parts := []string{fmt.Sprintf("# %s ticket %s: %s", tracker.Name(), i.Identifier, i.Title), i.URL, status, "", "## Description", description}
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

func PRPrompt(p github.PR, request string, previous []string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		request = "Read the pull request below and tell me how you want to proceed."
	}
	body, _ := shell.Run("", nil, time.Minute, "gh", "pr", "view", p.URL, "--json", "body", "-q", ".body")
	parts := append([]string{request, ""}, previous...)
	parts = append(parts, fmt.Sprintf("# Pull request %s: %s", github.PRKey(p), p.Title), p.URL, "", "## Description", strings.TrimSpace(string(body)))
	if len(p.Threads) > 0 {
		parts = append(parts, "", "## Open review threads")
		for _, t := range p.Threads {
			parts = append(parts, fmt.Sprintf("- %s:%d @%s: %s", t.Path, t.Line, t.Author, t.Body))
		}
	}
	return clipPrompt(strings.Join(parts, "\n"))
}

const MaxPromptBytes = 120_000
