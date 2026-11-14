package main

var (
	tracker    = "linear"
	trackerAPI = map[string]string{
		"linear": "To read or change tickets, call https://api.linear.app/graphql with $LINEAR_API_KEY as the Authorization header (no Bearer).",
		"jira":   "To read or change tickets, use the Jira Cloud REST API v3 at $JIRA_URL/rest/api/3 with basic auth $JIRA_EMAIL:$JIRA_API_TOKEN; moving a ticket to another status goes through GET and POST /rest/api/3/issue/<key>/transitions.",
	}
)

func trackerName() string {
	if tracker == "jira" {
		return "Jira"
	}
	return "Linear"
}

func fetchBoard(sources []string) ([]Issue, error) {
	if tracker == "jira" {
		return jiraBoard(sources)
	}
	return linearBoard(sources)
}

func fetchIssue(key string) (Issue, error) {
	if tracker == "jira" {
		return jiraIssueDetail(key)
	}
	return linearIssue(key)
}

func fetchDone(keys []string) ([]string, error) {
	if tracker == "jira" {
		return jiraDone(keys)
	}
	return linearDone(keys)
}

func fetchStates(teams []string) ([]State, error) {
	if tracker == "jira" {
		return jiraStates(teams)
	}
	return linearStates(teams)
}
