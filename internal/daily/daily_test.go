package daily

import (
	"encoding/json"
	"kta/internal/claude"
	"kta/internal/testenv"
	"kta/internal/tracker"
	"strings"
	"testing"
	"time"
)

func TestDailySince(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.Local) }
	cases := map[int]int{9: 6, 10: 9, 14: 13, 15: 13, 13: 12}
	for now, want := range cases {
		if got := Since(day(now)); got.Day() != want || got.Hour() != 0 {
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
	if got := claude.ContentText(raw("<command-name>x</command-name>")); got != "" {
		t.Errorf("contentText kept a tag message: %q", got)
	}
	blocks := []map[string]string{{"type": "text", "text": "hello"}, {"type": "tool_use", "text": "no"}, {"type": "text", "text": "<system>skip"}, {"type": "text", "text": "world"}}
	if got := claude.ContentText(raw(blocks)); got != "hello\nworld" {
		t.Errorf("contentText = %q", got)
	}
}

func TestGroupPrompt(t *testing.T) {
	a, b := tracker.Issue{Key: "A-1", Title: "Import sheets", Description: "upload"}, tracker.Issue{Key: "A-2", Title: "Export sheets"}
	p := claude.BuildPrompt(tracker.All[0], []tracker.Issue{a, b}, "", nil)
	for _, want := range []string{"Read the tickets below", "These 2 tickets are one piece of work", "# Linear ticket A-1: Import sheets", "# Linear ticket A-2: Export sheets"} {
		if !strings.Contains(p, want) {
			t.Errorf("group prompt misses %q", want)
		}
	}
	if got := ticketTopic(p); got != "Import sheets + Export sheets — upload" {
		t.Errorf("ticketTopic of a group = %q", got)
	}
}

func TestMain(m *testing.M) { testenv.Main(m) }

func TestSecretsAreScrubbed(t *testing.T) {
	in := "linear lin_api_abc12345 jira ATATT3xFfGF0Tk-9_x= trello ATTA0123456789abcdef keep ATTACK and Atlassian"
	want := "linear [secret] jira [secret] trello [secret] keep ATTACK and Atlassian"
	if got := secrets.ReplaceAllString(in, "[secret]"); got != want {
		t.Errorf("scrubbed = %q", got)
	}
}
