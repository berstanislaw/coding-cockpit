package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type PR struct {
	ID, Title, URL, Repo, Author, HeadSHA, CI, Decision string
	Number                                              int
	Mine                                                bool
	Threads                                             []Received
}

type Received struct {
	Path, Author, Body string
	Line               int
}

const prFragment = `fragment pr on PullRequest {
  id number title url headRefOid reviewDecision
  author { login }
  repository { nameWithOwner }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  reviewThreads(first: 50) { nodes { isResolved path line comments(first: 1) { nodes { author { login } body } } } }
}`

const prsQuery = prFragment + `
{
  viewer { organizations(first: 100) { nodes { login } } }
  mine: search(query: "is:pr is:open archived:false author:@me sort:updated-desc", type: ISSUE, first: 50) { nodes { ...pr } }
  recent: search(query: "is:pr author:@me sort:updated-desc", type: ISSUE, first: 30) { nodes { ... on PullRequest { labels(first: 10) { nodes { name } } } } }
}`

const teamQuery = prFragment + `
query($q: String!) { team: search(query: $q, type: ISSUE, first: 100) { nodes { ...pr } } }`

const sendQuery = `mutation($pr: ID!, $threads: [DraftPullRequestReviewThread]) {
  addPullRequestReview(input: { pullRequestId: $pr, threads: $threads }) { pullRequestReview { id } }
}`

func ghGraphQL(query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	b, err := run("", body, time.Minute, "gh", "api", "graphql", "--input", "-")
	var r struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if json.Unmarshal(b, &r) == nil && len(r.Errors) > 0 {
		return errors.New(r.Errors[0].Message)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(r.Data, out)
}

func prsCmd(labels []string) tea.Cmd {
	return func() tea.Msg {
		if _, err := exec.LookPath("gh"); err != nil {
			return prsMsg{err: errors.New("reviews need the GitHub CLI: install gh and run gh auth login")}
		}
		prs, pool, err := fetchPRs(labels)
		return prsMsg{prs, pool, err}
	}
}

func fetchPRs(labels []string) ([]PR, []string, error) {
	type node struct {
		ID, Title, URL, HeadRefOid, ReviewDecision string
		Number                                     int
		Author                                     struct{ Login string }
		Repository                                 struct{ NameWithOwner string }
		Commits                                    struct {
			Nodes []struct {
				Commit struct{ StatusCheckRollup *struct{ State string } }
			}
		}
		ReviewThreads struct {
			Nodes []struct {
				IsResolved bool
				Path       string
				Line       int
				Comments   struct {
					Nodes []struct {
						Author struct{ Login string }
						Body   string
					}
				}
			}
		}
		Labels struct{ Nodes []struct{ Name string } }
	}
	type search struct{ Nodes []node }
	var d struct {
		Viewer struct {
			Organizations struct{ Nodes []struct{ Login string } }
		}
		Mine, Team, Recent search
	}
	if err := ghGraphQL(prsQuery, nil, &d); err != nil {
		return nil, nil, err
	}
	var teamErr error
	if len(labels) > 0 && len(d.Viewer.Organizations.Nodes) > 0 {
		quoted := make([]string, len(labels))
		for i, l := range labels {
			quoted[i] = strconv.Quote(l)
		}
		q := "is:pr is:open draft:false archived:false -author:@me sort:updated-desc label:" + strings.Join(quoted, ",")
		for _, o := range d.Viewer.Organizations.Nodes {
			q += " org:" + o.Login
		}
		var t struct{ Team search }
		if teamErr = ghGraphQL(teamQuery, map[string]any{"q": q}, &t); teamErr == nil {
			d.Team = t.Team
		}
	}
	toPR := func(n node, mine bool) PR {
		p := PR{ID: n.ID, Title: n.Title, URL: n.URL, Repo: n.Repository.NameWithOwner, Author: n.Author.Login, HeadSHA: n.HeadRefOid, Decision: n.ReviewDecision, Number: n.Number, Mine: mine}
		if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
			p.CI = n.Commits.Nodes[0].Commit.StatusCheckRollup.State
		}
		for _, t := range n.ReviewThreads.Nodes {
			if !t.IsResolved && len(t.Comments.Nodes) > 0 {
				c := t.Comments.Nodes[0]
				p.Threads = append(p.Threads, Received{Path: t.Path, Line: t.Line, Author: c.Author.Login, Body: c.Body})
			}
		}
		return p
	}
	var prs []PR
	for _, n := range d.Mine.Nodes {
		prs = append(prs, toPR(n, true))
	}
	for _, n := range d.Team.Nodes {
		if n.URL != "" {
			prs = append(prs, toPR(n, false))
		}
	}
	var pool []string
	for _, n := range d.Recent.Nodes {
		for _, l := range n.Labels.Nodes {
			if !slices.Contains(pool, l.Name) {
				pool = append(pool, l.Name)
			}
		}
	}
	slices.Sort(pool)
	if teamErr != nil {
		teamErr = fmt.Errorf("team PRs: %w", teamErr)
	}
	return prs, pool, teamErr
}

func prKey(p PR) string {
	return p.Repo[strings.LastIndex(p.Repo, "/")+1:] + "#" + strconv.Itoa(p.Number)
}

func isPRKey(key string) bool {
	return strings.Contains(key, "#")
}

func sendCmd(p PR, r Review) tea.Cmd {
	threads := make([]map[string]any, len(r.Threads))
	for i, t := range r.Threads {
		th := map[string]any{"path": t.File, "line": t.HighlightEnd, "side": "RIGHT", "body": t.Comment}
		if t.HighlightStart < t.HighlightEnd {
			th["startLine"], th["startSide"] = t.HighlightStart, "RIGHT"
		}
		threads[i] = th
	}
	return func() tea.Msg {
		var out any
		return sentMsg{p.URL, ghGraphQL(sendQuery, map[string]any{"pr": p.ID, "threads": threads}, &out)}
	}
}
