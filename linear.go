package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

const boardQuery = `query($after: String) {
  viewer {
    assignedIssues(first: 100, after: $after, filter: { state: { type: { nin: ["completed", "canceled", "duplicate"] } } }) {
      pageInfo { hasNextPage endCursor }
      nodes {
        identifier title url priority updatedAt
        state { name type position color }
        team { key }
        labels(first: 10) { nodes { name color } }
      }
    }
  }
}`

const issueQuery = `query($id: String!) {
  issue(id: $id) {
    identifier title url priority description
    state { name type position color }
    team { key }
    labels(first: 10) { nodes { name color } }
    comments(first: 100) { nodes { body createdAt user { name } } }
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
	Comments struct{ Nodes []Comment }
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

func fetchBoard() ([]Issue, error) {
	var issues []Issue
	var after *string
	for {
		var d struct {
			Viewer struct {
				AssignedIssues struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []Issue
				}
			}
		}
		if err := linear(boardQuery, map[string]any{"after": after}, &d); err != nil {
			return nil, err
		}
		page := d.Viewer.AssignedIssues
		issues = append(issues, page.Nodes...)
		if !page.PageInfo.HasNextPage {
			return issues, nil
		}
		after = &page.PageInfo.EndCursor
	}
}

func fetchIssue(key string) (Issue, error) {
	var d struct{ Issue Issue }
	if err := linear(issueQuery, map[string]any{"id": key}, &d); err != nil {
		return Issue{}, err
	}
	sort.SliceStable(d.Issue.Comments.Nodes, func(a, b int) bool {
		return d.Issue.Comments.Nodes[a].CreatedAt < d.Issue.Comments.Nodes[b].CreatedAt
	})
	return d.Issue, nil
}
