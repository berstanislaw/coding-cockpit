package tracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (l linear) do(query string, vars map[string]any, out any) error {
	if l.key == "" {
		return errors.New("LINEAR_API_KEY is missing")
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", l.key)
	res, err := send("Linear", req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var r struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return fmt.Errorf("linear %d: %w", res.StatusCode, err)
	}
	if res.StatusCode != http.StatusOK || len(r.Errors) > 0 {
		msgs := make([]string, len(r.Errors))
		for i, e := range r.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("linear %d: %s", res.StatusCode, strings.Join(msgs, "; "))
	}
	return json.Unmarshal(r.Data, out)
}

func (l linear) Board(sources []string) ([]Issue, error) {
	me := map[string]any{"isMe": map[string]any{"eq": true}}
	var who []map[string]any
	for _, src := range Sources {
		if slices.Contains(sources, src.Key) {
			who = append(who, map[string]any{linearSources[src.Key]: me})
		}
	}
	filter := map[string]any{"or": who}
	var issues []Issue
	var after *string
	for {
		var d struct {
			Issues struct {
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
				Nodes []linearIssue
			}
		}
		if err := l.do(boardQuery, map[string]any{"after": after, "filter": filter}, &d); err != nil {
			return nil, err
		}
		page := d.Issues
		for _, n := range page.Nodes {
			issues = append(issues, n.issue())
		}
		if !page.PageInfo.HasNextPage {
			return issues, nil
		}
		after = &page.PageInfo.EndCursor
	}
}

func (l linear) Done(keys []string) ([]string, error) {
	var d struct {
		Issues struct{ Nodes []struct{ Identifier string } }
	}
	if err := l.do(doneQuery, map[string]any{"ids": keys}, &d); err != nil {
		return nil, err
	}
	out := make([]string, len(d.Issues.Nodes))
	for i, n := range d.Issues.Nodes {
		out[i] = n.Identifier
	}
	return out, nil
}

func (l linear) Me() (string, error) {
	var d struct{ Viewer struct{ Name string } }
	err := l.do(`{ viewer { name } }`, nil, &d)
	return d.Viewer.Name, err
}

func (l linear) States(teams []string) ([]State, error) {
	var d struct {
		Teams struct {
			Nodes []struct{ States struct{ Nodes []linearState } }
		}
	}
	if err := l.do(statesQuery, map[string]any{"keys": teams}, &d); err != nil {
		return nil, err
	}
	var out []State
	for _, t := range d.Teams.Nodes {
		for _, st := range t.States.Nodes {
			out = append(out, State(st))
		}
	}
	return out, nil
}

func (l linear) Issue(key string) (Issue, error) {
	var d struct{ Issue linearIssue }
	if err := l.do(issueQuery, map[string]any{"id": key}, &d); err != nil {
		return Issue{}, err
	}
	return d.Issue.issue(), nil
}

func (n linearIssue) issue() Issue {
	i := Issue{Key: n.Identifier, Title: n.Title, URL: n.URL, Description: n.Description, UpdatedAt: n.UpdatedAt, Priority: int(n.Priority), State: State(n.State), Team: n.Team.Key}
	for _, l := range n.Labels.Nodes {
		i.Labels = append(i.Labels, l.Name)
	}
	for _, c := range n.Comments.Nodes {
		author := "integration"
		if c.User != nil {
			author = c.User.Name
		}
		i.Comments = append(i.Comments, Comment{Author: author, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	for _, a := range n.Attachments.Nodes {
		i.Links = append(i.Links, a.URL)
	}
	slices.SortStableFunc(i.Comments, func(a, b Comment) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return i
}

const boardQuery = `query($after: String, $filter: IssueFilter) {
  issues(first: 100, after: $after, filter: $filter) {
    pageInfo { hasNextPage endCursor }
    nodes {
      identifier title url priority updatedAt
      state { name type position color }
      team { key }
      labels(first: 10) { nodes { name } }
      attachments { nodes { url } }
    }
  }
}`

const statesQuery = `query($keys: [String!]) {
  teams(filter: { key: { in: $keys } }) { nodes { states { nodes { name type position color } } } }
}`

const issueQuery = `query($id: String!) {
  issue(id: $id) {
    identifier title url priority description updatedAt
    state { name type position color }
    team { key }
    labels(first: 10) { nodes { name } }
    attachments { nodes { url } }
    comments(first: 100) { nodes { body createdAt user { name } } }
  }
}`

const doneQuery = `query($ids: [ID!]) {
  issues(first: 100, filter: { id: { in: $ids }, state: { type: { in: ["completed", "canceled", "duplicate"] } } }) {
    nodes { identifier }
  }
}`

var linearKind = Kind{
	Key:     "linear",
	Name:    "Linear",
	About:   "linear.app, with a personal API key",
	Team:    "Team",
	APIHint: "To read or change tickets, call https://api.linear.app/graphql with $LINEAR_API_KEY as the Authorization header (no Bearer).",
	Fields:  []Field{{Env: "LINEAR_API_KEY", Label: "API key", Hint: "Linear → Settings → Account → Security & access → Personal API keys", Secret: true}},
	open:    func(env map[string]string) backend { return linear{env["LINEAR_API_KEY"]} },
}

var linearSources = map[string]string{"assigned": "assignee", "created": "creator", "subscribed": "subscribers"}

type linear struct{ key string }

type linearState struct {
	Name, Type string
	Position   float64
	Color      string
}

type linearIssue struct {
	Identifier, Title, URL, Description string
	UpdatedAt                           time.Time
	Priority                            float64
	State                               linearState
	Team                                struct{ Key string }
	Labels                              struct{ Nodes []struct{ Name string } }
	Comments                            struct {
		Nodes []struct {
			Body      string
			CreatedAt time.Time
			User      *struct{ Name string }
		}
	}
	Attachments struct{ Nodes []struct{ URL string } }
}
