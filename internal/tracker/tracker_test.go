package tracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJiraSite(t *testing.T) {
	for in, want := range map[string]string{"acme": "https://acme.atlassian.net", "acme.atlassian.net": "https://acme.atlassian.net", " https://acme.atlassian.net/jira/software/projects/KAN/boards/1 ": "https://acme.atlassian.net", "http://jira.local:8080/": "http://jira.local:8080", "": ""} {
		if got := jiraSite(in); got != want {
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
	j := jira{srv.URL, "me@acme.com", "tok"}
	if name, err := j.Me(); err != nil || name != "Ana Lima" {
		t.Fatalf("jiraMyself = %q, %v", name, err)
	}
	issues, err := j.Board([]string{"assigned", "created"})
	if err != nil || len(issues) != 2 {
		t.Fatalf("jiraBoard = %+v, %v", issues, err)
	}
	if queries[0] != "(assignee = currentUser() OR reporter = currentUser()) ORDER BY updated DESC" {
		t.Errorf("jql = %q", queries[0])
	}
	first := issues[0]
	if first.State.Type != "started" || first.State.Name != "In Progress" || first.Priority != 2 || first.URL != srv.URL+"/browse/KAN-1" || first.Team != "KAN" || first.Labels[0] != "api" {
		t.Errorf("mapped issue = %+v", first)
	}
	if issues[1].State.Type != "unstarted" || issues[1].Priority != 0 {
		t.Errorf("second issue = %+v", issues[1])
	}
	detail, err := j.Issue("KAN-1")
	if err != nil || detail.Description != "Do it" || len(detail.Comments) != 1 || detail.Comments[0].Author != "Bia" || detail.Comments[0].Body != "ok" || detail.Comments[0].CreatedAt.Format(time.RFC3339) != "2026-11-13T10:00:00-03:00" {
		t.Errorf("detail = %+v, %v", detail, err)
	}
	if _, err := j.Issue("KAN-404"); err == nil || !strings.Contains(err.Error(), "Issue does not exist") {
		t.Errorf("missing issue error = %v", err)
	}
	j.token = "wrong"
	if _, err := j.Me(); err == nil || !strings.Contains(err.Error(), "didn't accept") {
		t.Errorf("bad token error = %v", err)
	}
}

func TestTrelloClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != `OAuth oauth_consumer_key="k", oauth_token="t"` {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "invalid token")
			return
		}
		list := map[string]string{"open": `{"name":"Doing","pos":2}`, "done": `{"name":"Done","pos":3}`, "archived": `{"name":"Old","pos":1,"closed":true}`}
		switch r.URL.Path {
		case "/1/members/me":
			fmt.Fprint(w, `{"fullName":"Ana Lima"}`)
		case "/1/members/me/boards":
			fmt.Fprint(w, `[{"id":"b1","name":"Shop","lists":[{"id":"l1","name":"Doing","pos":2},{"id":"l2","name":"Done","pos":3}]}]`)
		case "/1/members/me/cards":
			fmt.Fprint(w, `[{"name":"First","shortLink":"aaaa1111","shortUrl":"https://trello.com/c/aaaa1111","idBoard":"b1","idList":"l1","labels":[{"name":"api"},{"name":""}]},{"name":"Second","shortLink":"bbbb2222","idBoard":"b1","idList":"l2"},{"name":"Hidden","shortLink":"cccc3333","idBoard":"b9","idList":"l9"}]`)
		case "/1/cards/aaaa1111":
			fmt.Fprint(w, `{"name":"First","desc":"Do it","shortLink":"aaaa1111","list":`+list["open"]+`,"board":{"name":"Shop"},"actions":[{"date":"2026-11-14T10:00:00.000Z","data":{"text":"second"},"memberCreator":{"fullName":"Bia"}},{"date":"2026-11-13T10:00:00.000Z","data":{"text":"first"},"memberCreator":{"fullName":"Caio"}}]}`)
		case "/1/cards/bbbb2222":
			fmt.Fprint(w, `{"list":`+list["done"]+`}`)
		case "/1/cards/cccc3333":
			fmt.Fprint(w, `{"list":`+list["archived"]+`}`)
		case "/1/cards/eeee5555":
			fmt.Fprint(w, `{"closed":true,"list":`+list["open"]+`}`)
		case "/1/cards/ffff6666":
			fmt.Fprint(w, `{"list":`+list["open"]+`}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "The requested resource was not found.")
		}
	}))
	defer srv.Close()
	tr := trello{srv.URL + "/1", "k", "t"}
	if name, err := tr.Me(); err != nil || name != "Ana Lima" {
		t.Fatalf("trelloMyself = %q, %v", name, err)
	}
	issues, err := tr.Board([]string{"assigned"})
	if err != nil || len(issues) != 2 {
		t.Fatalf("board = %+v, %v", issues, err)
	}
	first := issues[0]
	if first.Key != "aaaa1111" || first.State.Name != "Doing" || first.State.Type != "started" || first.Team != "Shop" || first.URL != "https://trello.com/c/aaaa1111" || len(first.Labels) != 1 {
		t.Errorf("mapped card = %+v", first)
	}
	if issues[1].State.Type != "completed" {
		t.Errorf("a card in a Done list is completed: %+v", issues[1].State)
	}
	detail, err := tr.Issue("aaaa1111")
	if err != nil || detail.Description != "Do it" || detail.Team != "Shop" || len(detail.Comments) != 2 || detail.Comments[0].Body != "first" || detail.Comments[0].CreatedAt.Day() != 13 || detail.Comments[1].Author != "Bia" {
		t.Errorf("detail = %+v, %v", detail, err)
	}
	done, _ := tr.Done([]string{"bbbb2222", "cccc3333", "dddd4444", "eeee5555", "ffff6666", "KAN-1"})
	if strings.Join(done, ",") != "bbbb2222,cccc3333,dddd4444,eeee5555" {
		t.Errorf("done = %v", done)
	}
	if states, err := tr.States([]string{"Shop"}); err != nil || len(states) != 2 || states[1].Type != "completed" {
		t.Errorf("states = %+v, %v", states, err)
	}
	if _, err := tr.Issue("dddd4444"); err == nil || Retryable(err) {
		t.Errorf("a missing card is an error you can't retry away: %v", err)
	}
	if trelloKind.Supports("created") || !trelloKind.Supports("assigned") {
		t.Error("Trello boards only show the cards you're a member of")
	}
	tr.token = "wrong"
	if _, err := tr.Me(); err == nil || !strings.Contains(err.Error(), "didn't accept") {
		t.Errorf("bad token error = %v", err)
	}
}

func TestOpen(t *testing.T) {
	env := map[string]string{"JIRA_URL": "acme", "JIRA_EMAIL": "me@acme.com", "JIRA_API_TOKEN": "tok"}
	get := func(k string) string { return env[k] }
	if c, err := Open(get); err != nil || c.Key != "jira" || c.backend.(jira).url != "https://acme.atlassian.net" {
		t.Errorf("an old config with JIRA_URL and no TRACKER opens Jira: %v %+v", err, c)
	}
	env["TRACKER"] = "trello"
	if _, err := Open(get); err == nil || err.Error() != "Trello: API key is missing" {
		t.Errorf("a missing field fails: %v", err)
	}
	env["TRACKER"] = "asana"
	if _, err := Open(get); err == nil || !strings.Contains(err.Error(), "linear, jira, trello") {
		t.Errorf("unknown tracker error = %v", err)
	}
}

func TestLinearIssue(t *testing.T) {
	var n linearIssue
	json.Unmarshal([]byte(`{"identifier":"ENG-1","updatedAt":"2026-11-14T09:30:00.000Z","priority":2,"state":{"name":"Doing","type":"started","position":3},"team":{"key":"ENG"},"labels":{"nodes":[{"name":"api"}]},"comments":{"nodes":[{"body":"bot","createdAt":"2026-11-13T09:00:00.000Z"},{"body":"hi","createdAt":"2026-11-13T08:00:00.000Z","user":{"name":"Bia"}}]},"attachments":{"nodes":[{"url":"https://github.com/acme/shop/pull/7"}]}}`), &n)
	i := n.issue()
	if i.Key != "ENG-1" || i.Priority != 2 || i.State.Type != Started || i.Team != "ENG" || i.Labels[0] != "api" || i.Links[0] != "https://github.com/acme/shop/pull/7" {
		t.Errorf("linear issue = %+v", i)
	}
	if i.UpdatedAt.Hour() != 9 || i.Comments[0].CreatedAt.Day() != 13 {
		t.Errorf("linear times = %v %v", i.UpdatedAt, i.Comments[0].CreatedAt)
	}
	if i.Comments[0].Author != "Bia" || i.Comments[1].Author != "integration" {
		t.Errorf("comments = %+v", i.Comments)
	}
}

func TestRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := trello{srv.URL, "k", "t"}.Me()
	if !Retryable(err) || !strings.HasPrefix(err.Error(), "can't reach Trello") {
		t.Errorf("an unreachable tracker can be retried: %v", err)
	}
	if Retryable(statusError{500, "jira 500"}) {
		t.Error("an answer from the tracker isn't a network failure")
	}
}
