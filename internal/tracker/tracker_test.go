package tracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJiraSite(t *testing.T) {
	for in, want := range map[string]string{"acme": "https://acme.atlassian.net", "acme.atlassian.net": "https://acme.atlassian.net", " https://acme.atlassian.net/jira/software/projects/KAN/boards/1 ": "https://acme.atlassian.net", "http://jira.local:8080/": "http://jira.local:8080", "": ""} {
		if got := JiraSite(in); got != want {
			t.Errorf("jiraSite(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestADF(t *testing.T) {
	doc := `{"type":"doc","content":[
		{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Goal"}]},
		{"type":"paragraph","content":[{"type":"text","text":"Fix "},{"type":"text","text":"this","marks":[{"type":"strong"}]},{"type":"text","text":" now","marks":[{"type":"link","attrs":{"href":"https://x.dev"}}]},{"type":"hardBreak"},{"type":"mention","attrs":{"text":"@Ana"}}]},
		{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"nested"}]}]}]}]}]},
		{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]},
		{"type":"mediaSingle","content":[{"type":"media"}]}
	]}`
	want := "## Goal\n\nFix **this**[ now](https://x.dev)\n@Ana\n\n- one\n  - nested\n\n```go\nx := 1\n```\n\n[attachment]"
	if got := adfMarkdown(json.RawMessage(doc)); got != want {
		t.Errorf("adfMarkdown =\n%s\nwant\n%s", got, want)
	}
	if adfMarkdown(nil) != "" || adfMarkdown(json.RawMessage("null")) != "" {
		t.Error("no description gives an empty string")
	}
}

func TestJiraClient(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "me@acme.com" || p != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/myself":
			fmt.Fprint(w, `{"displayName":"Ana Lima"}`)
		case "/rest/api/3/search/jql":
			var body struct{ JQL, NextPageToken string }
			json.NewDecoder(r.Body).Decode(&body)
			queries = append(queries, body.JQL)
			if body.NextPageToken == "" {
				fmt.Fprint(w, `{"issues":[{"key":"KAN-1","fields":{"summary":"First","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"priority":{"name":"High"},"project":{"key":"KAN"},"labels":["api"]}}],"nextPageToken":"p2"}`)
				return
			}
			fmt.Fprint(w, `{"issues":[{"key":"KAN-2","fields":{"summary":"Second","status":{"name":"To Do","statusCategory":{"key":"new"}},"project":{"key":"KAN"}}}]}`)
		case "/rest/api/3/issue/KAN-1":
			fmt.Fprint(w, `{"key":"KAN-1","fields":{"summary":"First","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"project":{"key":"KAN"},"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Do it"}]}]},"comment":{"comments":[{"author":{"displayName":"Bia"},"created":"2026-11-13T10:00:00.000-0300","body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"ok"}]}]}}]}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["Issue does not exist"]}`)
		}
	}))
	defer srv.Close()
	oldURL, oldEmail, oldToken := JiraURL, JiraEmail, JiraToken
	t.Cleanup(func() { JiraURL, JiraEmail, JiraToken = oldURL, oldEmail, oldToken })
	JiraURL, JiraEmail, JiraToken = srv.URL, "me@acme.com", "tok"
	if name, err := JiraMyself(); err != nil || name != "Ana Lima" {
		t.Fatalf("jiraMyself = %q, %v", name, err)
	}
	issues, err := jiraBoard([]string{"assigned", "created"})
	if err != nil || len(issues) != 2 {
		t.Fatalf("jiraBoard = %+v, %v", issues, err)
	}
	if queries[0] != "(assignee = currentUser() OR reporter = currentUser()) ORDER BY updated DESC" {
		t.Errorf("jql = %q", queries[0])
	}
	first := issues[0]
	if first.State.Type != "started" || first.State.Name != "In Progress" || first.Priority != 2 || first.URL != srv.URL+"/browse/KAN-1" || first.Team.Key != "KAN" || first.Labels.Nodes[0].Name != "api" {
		t.Errorf("mapped issue = %+v", first)
	}
	if issues[1].State.Type != "unstarted" || issues[1].Priority != 0 {
		t.Errorf("second issue = %+v", issues[1])
	}
	detail, err := jiraIssueDetail("KAN-1")
	if err != nil || detail.Description != "Do it" || len(detail.Comments.Nodes) != 1 || detail.Comments.Nodes[0].User.Name != "Bia" || detail.Comments.Nodes[0].Body != "ok" {
		t.Errorf("detail = %+v, %v", detail, err)
	}
	if _, err := jiraIssueDetail("KAN-404"); err == nil || !strings.Contains(err.Error(), "Issue does not exist") {
		t.Errorf("missing issue error = %v", err)
	}
	JiraToken = "wrong"
	if _, err := JiraMyself(); err == nil || !strings.Contains(err.Error(), "didn't accept") {
		t.Errorf("bad token error = %v", err)
	}
}
