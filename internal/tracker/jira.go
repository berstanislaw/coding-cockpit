package tracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

func jiraSite(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	if !strings.Contains(u.Host, ".") {
		u.Host += ".atlassian.net"
	}
	return u.Scheme + "://" + u.Host
}

func (j jira) do(method, path string, body, out any) error {
	if j.url == "" {
		return errors.New("JIRA_URL is missing")
	}
	var in io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		in = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, j.url+path, in)
	if err != nil {
		return err
	}
	req.SetBasicAuth(j.email, j.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := send("Jira", req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return errors.New("Jira didn't accept the email and API token")
	}
	if res.StatusCode >= 300 {
		var e struct {
			ErrorMessages []string
			Errors        map[string]string
		}
		json.NewDecoder(res.Body).Decode(&e)
		msgs := e.ErrorMessages
		for _, m := range e.Errors {
			msgs = append(msgs, m)
		}
		if len(msgs) == 0 {
			return statusError{res.StatusCode, fmt.Sprintf("jira %d at %s", res.StatusCode, j.url)}
		}
		return statusError{res.StatusCode, fmt.Sprintf("jira %d: %s", res.StatusCode, strings.Join(msgs, "; "))}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func jiraState(s jiraStatus) State {
	st := jiraCategory[s.StatusCategory.Key]
	st.Name = s.Name
	return st
}

func (ji jiraIssue) issue(site string) Issue {
	f := ji.Fields
	i := Issue{Key: ji.Key, Title: f.Summary, URL: site + "/browse/" + ji.Key, UpdatedAt: jiraTime(f.Updated), Description: adfMarkdown(f.Description), State: jiraState(f.Status), Team: f.Project.Key, Labels: f.Labels}
	if f.Priority != nil {
		i.Priority = jiraPriority[f.Priority.Name]
	}
	for _, c := range f.Comment.Comments {
		i.Comments = append(i.Comments, Comment{Author: c.Author.DisplayName, Body: adfMarkdown(c.Body), CreatedAt: jiraTime(c.Created)})
	}
	return i
}

func jiraTime(s string) time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.000-0700", s)
	return t
}

func jiraJQL(sources []string) string {
	var who []string
	for _, s := range sources {
		if f := jiraSources[s]; f != "" {
			who = append(who, f+" = currentUser()")
		}
	}
	return "(" + strings.Join(who, " OR ") + ") ORDER BY updated DESC"
}

func (j jira) search(jql string) ([]jiraIssue, error) {
	var all []jiraIssue
	token := ""
	for {
		body := map[string]any{"jql": jql, "fields": strings.Split(jiraFields, ","), "maxResults": 100}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page struct {
			Issues        []jiraIssue
			NextPageToken string
		}
		if err := j.do("POST", "/rest/api/3/search/jql", body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Issues...)
		if page.NextPageToken == "" || page.NextPageToken == token {
			return all, nil
		}
		token = page.NextPageToken
	}
}

func (j jira) Board(sources []string) ([]Issue, error) {
	found, err := j.search(jiraJQL(sources))
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, len(found))
	for k, ji := range found {
		issues[k] = ji.issue(j.url)
	}
	return issues, nil
}

func (j jira) Issue(key string) (Issue, error) {
	var ji jiraIssue
	if err := j.do("GET", "/rest/api/3/issue/"+url.PathEscape(key)+"?fields="+jiraFields+",description,comment", nil, &ji); err != nil {
		return Issue{}, err
	}
	return ji.issue(j.url), nil
}

func (j jira) Done(keys []string) ([]string, error) {
	var valid []string
	for _, k := range keys {
		if jiraKey.MatchString(k) {
			valid = append(valid, k)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	found, err := j.search("key in (" + strings.Join(valid, ", ") + ") AND statusCategory = Done")
	if err == nil {
		done := make([]string, len(found))
		for k, ji := range found {
			done[k] = ji.Key
		}
		return done, nil
	}
	var done []string
	for _, k := range valid {
		var ji jiraIssue
		err := j.do("GET", "/rest/api/3/issue/"+k+"?fields=status", nil, &ji)
		if notFound(err) || (err == nil && ji.Fields.Status.StatusCategory.Key == "done") {
			done = append(done, k)
		}
	}
	return done, nil
}

func (j jira) States(projects []string) ([]State, error) {
	var out []State
	seen := map[string]bool{}
	for _, p := range projects {
		var types []struct{ Statuses []jiraStatus }
		if err := j.do("GET", "/rest/api/3/project/"+url.PathEscape(p)+"/statuses", nil, &types); err != nil {
			return nil, err
		}
		for _, t := range types {
			for _, s := range t.Statuses {
				if !seen[s.Name] {
					seen[s.Name] = true
					st := jiraState(s)
					st.Position = float64(len(out))
					out = append(out, st)
				}
			}
		}
	}
	return out, nil
}

func (j jira) Me() (string, error) {
	var me struct{ DisplayName string }
	err := j.do("GET", "/rest/api/3/myself", nil, &me)
	return me.DisplayName, err
}

var jiraKind = Kind{
	Key:     "jira",
	Name:    "Jira",
	About:   "Jira Cloud, with your email and an API token",
	Team:    "Project",
	APIHint: "To read or change tickets, use the Jira Cloud REST API v3 at $JIRA_URL/rest/api/3 with basic auth $JIRA_EMAIL:$JIRA_API_TOKEN; moving a ticket to another status goes through GET and POST /rest/api/3/issue/<key>/transitions.",
	Fields: []Field{
		{Env: "JIRA_URL", Label: "Site", Hint: "the address of your Jira, like acme.atlassian.net (acme works too)", Placeholder: "acme.atlassian.net", clean: jiraSite},
		{Env: "JIRA_EMAIL", Label: "Email", Hint: "the email you sign in to Atlassian with"},
		{Env: "JIRA_API_TOKEN", Label: "API token", Hint: "id.atlassian.com → Security → Create and manage API tokens", Secret: true},
	},
	open: func(env map[string]string) backend {
		return jira{env["JIRA_URL"], env["JIRA_EMAIL"], env["JIRA_API_TOKEN"]}
	},
}

type jira struct{ url, email, token string }

var (
	jiraPriority = map[string]int{"Highest": 1, "High": 2, "Medium": 3, "Low": 4, "Lowest": 4}
	jiraCategory = map[string]State{
		"new":           {Type: Unstarted, Color: "#8b919c"},
		"indeterminate": {Type: Started, Color: "#d29922"},
		"done":          {Type: Completed, Color: "#3fb950"},
	}
	jiraSources = map[string]string{"assigned": "assignee", "created": "reporter", "subscribed": "watcher"}
	jiraKey     = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-\d+$`)
)

const jiraFields = "summary,status,priority,updated,project,labels"

type jiraStatus struct {
	Name           string
	StatusCategory struct{ Key string }
}

type jiraIssue struct {
	Key    string
	Fields struct {
		Summary     string
		Updated     string
		Status      jiraStatus
		Priority    *struct{ Name string }
		Project     struct{ Key string }
		Labels      []string
		Description json.RawMessage
		Comment     struct {
			Comments []struct {
				Author  struct{ DisplayName string }
				Body    json.RawMessage
				Created string
			}
		}
	}
}
