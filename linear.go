package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

const boardQuery = `query($after: String, $filter: IssueFilter) {
  issues(first: 100, after: $after, filter: $filter) {
    pageInfo { hasNextPage endCursor }
    nodes {
      identifier title url priority updatedAt
      state { name type position color }
      team { key }
      labels(first: 10) { nodes { name color } }
      attachments { nodes { url } }
    }
  }
}`

const statesQuery = `query($keys: [String!]) {
  teams(filter: { key: { in: $keys } }) { nodes { states { nodes { name type position color } } } }
}`

const issueQuery = `query($id: String!) {
  issue(id: $id) {
    identifier title url priority description
    state { name type position color }
    team { key }
    labels(first: 10) { nodes { name color } }
    attachments { nodes { url } }
    comments(first: 100) { nodes { body createdAt user { name } } }
  }
}`

const doneQuery = `query($ids: [ID!]) {
  issues(first: 100, filter: { id: { in: $ids }, state: { type: { in: ["completed", "canceled", "duplicate"] } } }) {
    nodes { identifier }
  }
}`

type State struct {
	Name     string
	Type     string
	Position float64
	Color    string
}

type Comment struct {
	Body      string
	CreatedAt string
	User      *struct{ Name string }
}

type Issue struct {
	Identifier  string
	Title       string
	URL         string
	Priority    float64
	UpdatedAt   string
	Description string
	State       State
	Team        struct{ Key string }
	Labels      struct {
		Nodes []struct{ Name, Color string }
	}
	Comments    struct{ Nodes []Comment }
	Attachments struct {
		Nodes []struct{ URL string }
	}
}

var (
	apiKey     string
	httpClient = &http.Client{Timeout: 20 * time.Second}
)

func linear(query string, vars map[string]any, out any) error {
	if apiKey == "" {
		return fmt.Errorf("LINEAR_API_KEY missing in %s", envFile)
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
	req.Header.Set("authorization", apiKey)
	res, err := httpClient.Do(req)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("Linear didn't respond in %s · press r to retry", httpClient.Timeout)
	}
	if err != nil {
		return fmt.Errorf("can't reach Linear (%v) · press r to retry", errors.Unwrap(err))
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

func linearBoard(sources []string) ([]Issue, error) {
	me := map[string]any{"isMe": map[string]any{"eq": true}}
	var who []map[string]any
	for _, src := range ticketSources {
		if slices.Contains(sources, src.key) {
			who = append(who, map[string]any{src.field: me})
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
				Nodes []Issue
			}
		}
		if err := linear(boardQuery, map[string]any{"after": after, "filter": filter}, &d); err != nil {
			return nil, err
		}
		page := d.Issues
		issues = append(issues, page.Nodes...)
		if !page.PageInfo.HasNextPage {
			return issues, nil
		}
		after = &page.PageInfo.EndCursor
	}
}

func linearDone(keys []string) ([]string, error) {
	var d struct {
		Issues struct{ Nodes []struct{ Identifier string } }
	}
	if err := linear(doneQuery, map[string]any{"ids": keys}, &d); err != nil {
		return nil, err
	}
	out := make([]string, len(d.Issues.Nodes))
	for i, n := range d.Issues.Nodes {
		out[i] = n.Identifier
	}
	return out, nil
}

func linearViewer() (string, error) {
	var d struct{ Viewer struct{ Name string } }
	err := linear(`{ viewer { name } }`, nil, &d)
	return d.Viewer.Name, err
}

func linearStates(teams []string) ([]State, error) {
	var d struct {
		Teams struct {
			Nodes []struct{ States struct{ Nodes []State } }
		}
	}
	if err := linear(statesQuery, map[string]any{"keys": teams}, &d); err != nil {
		return nil, err
	}
	var out []State
	for _, t := range d.Teams.Nodes {
		out = append(out, t.States.Nodes...)
	}
	return out, nil
}

func linearIssue(key string) (Issue, error) {
	var d struct{ Issue Issue }
	if err := linear(issueQuery, map[string]any{"id": key}, &d); err != nil {
		return Issue{}, err
	}
	sort.SliceStable(d.Issue.Comments.Nodes, func(a, b int) bool {
		return d.Issue.Comments.Nodes[a].CreatedAt < d.Issue.Comments.Nodes[b].CreatedAt
	})
	return d.Issue, nil
}
