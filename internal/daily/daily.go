package daily

import (
	"bufio"
	"encoding/json"
	"fmt"
	"kta/internal/claude"
	"kta/internal/config"
	"kta/internal/shell"
	"kta/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func Since(now time.Time) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

func Cmd(force bool, lang store.Language) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		file := filepath.Join(config.DailyDir, now.Format("2006-01-02")+"."+lang.Code+".md")
		if st, err := os.Stat(file); err == nil && !force {
			b, err := os.ReadFile(file)
			return Msg{string(b), st.ModTime(), err}
		}
		notes := Notes(Since(now), now, lang)
		text := "_no Claude sessions since " + Since(now).Format("Mon 02/01") + "_"
		if notes != "" {
			var err error
			if text, err = summarize(notes, lang); err != nil {
				return Msg{Err: err}
			}
		}
		if err := os.MkdirAll(config.DailyDir, 0o700); err != nil {
			return Msg{Err: err}
		}
		return Msg{text, time.Now(), os.WriteFile(file, []byte(text), 0o600)}
	}
}

func summarize(notes string, lang store.Language) (string, error) {
	out, err := shell.Run(config.ConfDir, []byte(notes), 5*time.Minute, "claude", "-p", "--model", "haiku", "--no-session-persistence", "--setting-sources", "project", "--strict-mcp-config", "--tools", "", "--system-prompt", fmt.Sprintf(dailyPrompt, lang.Prompt, lang.Today, lang.Examples))
	if err != nil {
		return "", fmt.Errorf("daily summary failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func Notes(since, now time.Time, lang store.Language) string {
	files, _ := filepath.Glob(filepath.Join(config.ClaudeSessionsDir, "*.jsonl"))
	days := map[string][]string{}
	for _, f := range files {
		if st, err := os.Stat(f); err != nil || st.ModTime().Before(since) {
			continue
		}
		for day, note := range sessionNotes(f, since) {
			days[day] = append(days[day], note)
		}
	}
	keys := make([]string, 0, len(days))
	for day := range days {
		keys = append(keys, day)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, day := range keys {
		label := lang.Today
		if d, _ := time.ParseInLocation("2006-01-02", day, now.Location()); day != now.Format("2006-01-02") {
			label = lang.Weekdays[d.Weekday()] + " (" + d.Format("02/01") + ")"
		}
		fmt.Fprintf(&b, "# %s\n\n%s\n", label, strings.Join(days[day], "\n"))
	}
	text := secrets.ReplaceAllString(b.String(), "[secret]")
	if len(text) > claude.MaxPromptBytes {
		text = strings.ToValidUTF8(text[:claude.MaxPromptBytes], "")
	}
	return text
}

func sessionNotes(file string, since time.Time) map[string]string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	topic := ""
	asked, replied := map[string][]string{}, map[string][]string{}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var l struct {
			Type             string
			Timestamp        time.Time
			IsMeta           bool
			IsCompactSummary bool
			Message          struct{ Content json.RawMessage }
		}
		if json.Unmarshal(line, &l) == nil && !l.IsMeta && !l.IsCompactSummary && (l.Type == "user" || l.Type == "assistant") {
			if text := claude.ContentText(l.Message.Content); text != "" {
				if l.Type == "user" && topic == "" {
					topic = ticketTopic(text)
				}
				if !l.Timestamp.Before(since) {
					day := l.Timestamp.Local().Format("2006-01-02")
					if l.Type == "user" {
						asked[day] = append(asked[day], clip(request(text), 400))
					} else {
						replied[day] = append(replied[day], clip(text, 500))
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	out := map[string]string{}
	for day, prompts := range asked {
		lines := []string{"## Session"}
		if topic != "" {
			lines = append(lines, "Ticket: "+topic)
		}
		for _, p := range prompts[max(0, len(prompts)-10):] {
			lines = append(lines, "- I asked: "+p)
		}
		for _, r := range replied[day][max(0, len(replied[day])-3):] {
			lines = append(lines, "- Claude: "+r)
		}
		out[day] = strings.Join(lines, "\n") + "\n"
	}
	return out
}

func ticketTopic(text string) string {
	var titles []string
	for _, m := range ticketHeader.FindAllStringSubmatch(text, -1) {
		titles = append(titles, m[1])
	}
	if titles == nil {
		return ""
	}
	_, desc, _ := strings.Cut(text, "\n## Description\n")
	for _, marker := range []string{"\n## Comments", "\n# Linear ticket ", "\n# Jira ticket ", "\n## kta"} {
		desc, _, _ = strings.Cut(desc, marker)
	}
	return clip(strings.Join(titles, " + "), 300) + " — " + clip(desc, 600)
}

func request(text string) string {
	for _, marker := range []string{"\n## Previous sessions on this ticket", "\n# Linear ticket ", "\n# Jira ticket "} {
		text, _, _ = strings.Cut(text, marker)
	}
	return text
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

const dailyPrompt = `You write my daily stand-up notes. The input has my Claude Code work sessions grouped by day: for each session, the ticket it was about (when there is one), what I asked and Claude's last replies.

Write the summary in %s, even when the sessions are in another language. For each day, write its heading exactly as given in bold (like **%s**) on its own line, followed by a bullet list. One bullet per card or subject, never one per task: merge everything done on the same card or subject that day into a single bullet. Keep each bullet dumb and short, at the level of these examples, saying what the work was about and not the details of what was done: %s. Describe the subject so anyone on the team understands it: never write ticket identifiers (like KAN-123), session names, people, file paths, ids, URLs, keys or commit hashes. Leave out sessions where nothing real happened, like tests and quick questions. Reply with only the headings and bullets.`

var (
	ticketHeader = regexp.MustCompile(`(?m)^# (?:Linear|Jira) ticket \S+: (.+)$`)
	secrets      = regexp.MustCompile(`\b(lin_api|sk-ant|ghp|gho|github_pat|xox[abp])[-_A-Za-z0-9]{8,}`)
)

type Msg struct {
	Text string
	At   time.Time
	Err  error
}
