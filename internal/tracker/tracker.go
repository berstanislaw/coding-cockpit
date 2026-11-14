package tracker

import (
	"errors"
	"fmt"
	"os"
)

func Configure(kind string, jira bool) error {
	if Kind = kind; Kind == "" {
		Kind = "linear"
		if jira {
			Kind = "jira"
		}
	}
	switch Kind {
	case "linear":
		if LinearKey = os.Getenv("LINEAR_API_KEY"); LinearKey == "" {
			return errors.New("Linear key missing")
		}
	case "jira":
		JiraURL, JiraEmail, JiraToken = JiraSite(os.Getenv("JIRA_URL")), os.Getenv("JIRA_EMAIL"), os.Getenv("JIRA_API_TOKEN")
		if JiraURL == "" || JiraEmail == "" || JiraToken == "" {
			return errors.New("Jira account missing")
		}
	default:
		return fmt.Errorf("TRACKER=%s isn't linear or jira", Kind)
	}
	return nil
}

func Name() string {
	if Kind == "jira" {
		return "Jira"
	}
	return "Linear"
}

func FetchBoard(sources []string) ([]Issue, error) {
	if Kind == "jira" {
		return jiraBoard(sources)
	}
	return linearBoard(sources)
}

func FetchIssue(key string) (Issue, error) {
	if Kind == "jira" {
		return jiraIssueDetail(key)
	}
	return linearIssue(key)
}

func FetchDone(keys []string) ([]string, error) {
	if Kind == "jira" {
		return jiraDone(keys)
	}
	return linearDone(keys)
}

func FetchStates(teams []string) ([]State, error) {
	if Kind == "jira" {
		return jiraStates(teams)
	}
	return linearStates(teams)
}

var Sources = []struct{ Key, Label, field string }{
	{"assigned", "assigned to me", "assignee"},
	{"created", "created by me", "creator"},
	{"subscribed", "subscribed to", "subscribers"},
}

var Kind = "linear"

var APIHint = map[string]string{
	"linear": "To read or change tickets, call https://api.linear.app/graphql with $LINEAR_API_KEY as the Authorization header (no Bearer).",
	"jira":   "To read or change tickets, use the Jira Cloud REST API v3 at $JIRA_URL/rest/api/3 with basic auth $JIRA_EMAIL:$JIRA_API_TOKEN; moving a ticket to another status goes through GET and POST /rest/api/3/issue/<key>/transitions.",
}
