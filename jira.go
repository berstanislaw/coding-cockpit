package main

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

var (
	jiraURL, jiraEmail, jiraToken string

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

func jiraDo(method, path string, body, out any) error {
	if jiraURL == "" {
		return fmt.Errorf("JIRA_URL missing in %s", envFile)
	}
	var in io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		in = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, jiraURL+path, in)
	if err != nil {
		return err
	}
	req.SetBasicAuth(jiraEmail, jiraToken)
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
		return fmt.Errorf("can't reach %s (%v)", jiraURL, errors.Unwrap(err))
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
			return fmt.Errorf("jira %d at %s", res.StatusCode, jiraURL)
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
	i := Issue{Identifier: j.Key, Title: f.Summary, URL: jiraURL + "/browse/" + j.Key, UpdatedAt: f.Updated, Description: adfMarkdown(f.Description), State: jiraState(f.Status)}
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

func jiraMyself() (string, error) {
	var me struct{ DisplayName string }
	err := jiraDo("GET", "/rest/api/3/myself", nil, &me)
	return me.DisplayName, err
}

type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []adfNode      `json:"marks"`
	Content []adfNode      `json:"content"`
}

func adfMarkdown(raw json.RawMessage) string {
	var doc adfNode
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return strings.TrimSpace(adfBlocks(doc.Content))
}

func adfBlocks(nodes []adfNode) string { return adfJoin(nodes, "\n\n") }

func adfJoin(nodes []adfNode, sep string) string {
	var out []string
	for _, n := range nodes {
		if s := adfBlock(n); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}

func adfBlock(n adfNode) string {
	switch n.Type {
	case "paragraph":
		return adfInline(n.Content)
	case "heading":
		level, _ := n.Attrs["level"].(float64)
		return strings.Repeat("#", max(1, int(level))) + " " + adfInline(n.Content)
	case "bulletList", "orderedList":
		items := make([]string, len(n.Content))
		for k, item := range n.Content {
			marker := "- "
			if n.Type == "orderedList" {
				marker = fmt.Sprintf("%d. ", k+1)
			}
			items[k] = marker + strings.ReplaceAll(adfJoin(item.Content, "\n"), "\n", "\n"+strings.Repeat(" ", len(marker)))
		}
		return strings.Join(items, "\n")
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		return "```" + lang + "\n" + adfInline(n.Content) + "\n```"
	case "blockquote", "panel":
		return "> " + strings.ReplaceAll(adfBlocks(n.Content), "\n", "\n> ")
	case "rule":
		return "---"
	case "table":
		var rows []string
		for _, row := range n.Content {
			cells := make([]string, len(row.Content))
			for k, cell := range row.Content {
				cells[k] = strings.ReplaceAll(adfBlocks(cell.Content), "\n", " ")
			}
			rows = append(rows, "| "+strings.Join(cells, " | ")+" |")
			if len(rows) == 1 {
				rows = append(rows, "|"+strings.Repeat(" --- |", len(cells)))
			}
		}
		return strings.Join(rows, "\n")
	case "mediaSingle", "mediaGroup", "media":
		return "[attachment]"
	case "text", "hardBreak", "mention", "emoji", "status", "inlineCard":
		return adfInline([]adfNode{n})
	}
	return adfBlocks(n.Content)
}

func adfInline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			t := n.Text
			for _, mk := range n.Marks {
				switch mk.Type {
				case "strong":
					t = "**" + t + "**"
				case "em":
					t = "*" + t + "*"
				case "code":
					t = "`" + t + "`"
				case "strike":
					t = "~~" + t + "~~"
				case "link":
					if href, _ := mk.Attrs["href"].(string); href != "" {
						t = "[" + t + "](" + href + ")"
					}
				}
			}
			b.WriteString(t)
		case "hardBreak":
			b.WriteString("\n")
		case "mention", "emoji", "status":
			text, _ := n.Attrs["text"].(string)
			if text == "" {
				text, _ = n.Attrs["shortName"].(string)
			}
			b.WriteString(text)
		case "inlineCard", "blockCard":
			u, _ := n.Attrs["url"].(string)
			b.WriteString(u)
		default:
			b.WriteString(adfInline(n.Content))
		}
	}
	return b.String()
}
