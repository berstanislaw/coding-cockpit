package tracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func JiraSite(s string) string {
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

func jiraDo(method, path string, body, out any) error {
	if JiraURL == "" {
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
	req, err := http.NewRequest(method, JiraURL+path, in)
	if err != nil {
		return err
	}
	req.SetBasicAuth(JiraEmail, JiraToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := httpClient.Do(req)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("Jira didn't respond in %s · press r to retry", httpClient.Timeout)
	}
	if err != nil {
		return fmt.Errorf("can't reach %s (%v)", JiraURL, errors.Unwrap(err))
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
			return fmt.Errorf("jira %d at %s", res.StatusCode, JiraURL)
		}
		return fmt.Errorf("jira %d: %s", res.StatusCode, strings.Join(msgs, "; "))
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

func (j jiraIssue) issue() Issue {
	f := j.Fields
	i := Issue{Identifier: j.Key, Title: f.Summary, URL: JiraURL + "/browse/" + j.Key, UpdatedAt: f.Updated, Description: adfMarkdown(f.Description), State: jiraState(f.Status)}
	if f.Priority != nil {
		i.Priority = jiraPriority[f.Priority.Name]
	}
	i.Team.Key = f.Project.Key
	for _, l := range f.Labels {
		i.Labels.Nodes = append(i.Labels.Nodes, struct{ Name, Color string }{l, ""})
	}
	for _, c := range f.Comment.Comments {
		i.Comments.Nodes = append(i.Comments.Nodes, Comment{Body: adfMarkdown(c.Body), CreatedAt: c.Created, User: &struct{ Name string }{c.Author.DisplayName}})
	}
	return i
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

func jiraSearch(jql string) ([]jiraIssue, error) {
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
		if err := jiraDo("POST", "/rest/api/3/search/jql", body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Issues...)
		if page.NextPageToken == "" || page.NextPageToken == token {
			return all, nil
		}
		token = page.NextPageToken
	}
}

func jiraBoard(sources []string) ([]Issue, error) {
	found, err := jiraSearch(jiraJQL(sources))
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, len(found))
	for k, j := range found {
		issues[k] = j.issue()
	}
	return issues, nil
}

func jiraIssueDetail(key string) (Issue, error) {
	var j jiraIssue
	if err := jiraDo("GET", "/rest/api/3/issue/"+url.PathEscape(key)+"?fields="+jiraFields+",description,comment", nil, &j); err != nil {
		return Issue{}, err
	}
	return j.issue(), nil
}

func jiraDone(keys []string) ([]string, error) {
	var valid []string
	for _, k := range keys {
		if jiraKey.MatchString(k) {
			valid = append(valid, k)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	found, err := jiraSearch("key in (" + strings.Join(valid, ", ") + ") AND statusCategory = Done")
	if err == nil {
		done := make([]string, len(found))
		for k, j := range found {
			done[k] = j.Key
		}
		return done, nil
	}
	var done []string
	for _, k := range valid {
		var j jiraIssue
		err := jiraDo("GET", "/rest/api/3/issue/"+k+"?fields=status", nil, &j)
		if (err != nil && strings.Contains(err.Error(), "jira 404")) || (err == nil && j.Fields.Status.StatusCategory.Key == "done") {
			done = append(done, k)
		}
	}
	return done, nil
}

func jiraStates(projects []string) ([]State, error) {
	var out []State
	seen := map[string]bool{}
	for _, p := range projects {
		var types []struct{ Statuses []jiraStatus }
		if err := jiraDo("GET", "/rest/api/3/project/"+url.PathEscape(p)+"/statuses", nil, &types); err != nil {
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

func JiraMyself() (string, error) {
	var me struct{ DisplayName string }
	err := jiraDo("GET", "/rest/api/3/myself", nil, &me)
	return me.DisplayName, err
}

var (
	JiraURL, JiraEmail, JiraToken string

	jiraPriority = map[string]float64{"Highest": 1, "High": 2, "Medium": 3, "Low": 4, "Lowest": 4}
	jiraCategory = map[string]State{
		"new":           {Type: "unstarted", Color: "#8b919c"},
		"indeterminate": {Type: "started", Color: "#d29922"},
		"done":          {Type: "completed", Color: "#3fb950"},
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
