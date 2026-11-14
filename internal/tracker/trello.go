package tracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

func (t trello) do(path string, out any) error {
	req, err := http.NewRequest("GET", t.api+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf(`OAuth oauth_consumer_key="%s", oauth_token="%s"`, t.key, t.token))
	res, err := send("Trello", req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("Trello didn't accept the API key and token")
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return statusError{res.StatusCode, fmt.Sprintf("trello %d: %s", res.StatusCode, strings.TrimSpace(string(b)))}
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func trelloState(l trelloList) State {
	if l.Closed || trelloDoneList.MatchString(l.Name) {
		return State{Name: l.Name, Type: Completed, Position: l.Pos, Color: "#3fb950"}
	}
	return State{Name: l.Name, Type: Started, Position: l.Pos, Color: "#8b919c"}
}

func (c trelloCard) issue(l trelloList, board string) Issue {
	i := Issue{Key: c.ShortLink, Title: c.Name, URL: c.ShortURL, UpdatedAt: c.DateLastActivity, Description: c.Desc, State: trelloState(l), Team: board}
	for _, label := range c.Labels {
		if label.Name != "" {
			i.Labels = append(i.Labels, label.Name)
		}
	}
	for _, a := range slices.Backward(c.Actions) {
		i.Comments = append(i.Comments, Comment{Author: a.MemberCreator.FullName, Body: a.Data.Text, CreatedAt: a.Date})
	}
	return i
}

func (t trello) boards() ([]trelloBoardInfo, error) {
	var boards []trelloBoardInfo
	err := t.do("/members/me/boards?filter=open&fields=name&lists=open", &boards)
	return boards, err
}

func (t trello) Board([]string) ([]Issue, error) {
	boards, err := t.boards()
	if err != nil {
		return nil, err
	}
	lists, names := map[string]trelloList{}, map[string]string{}
	for _, b := range boards {
		names[b.ID] = b.Name
		for _, l := range b.Lists {
			lists[l.ID] = l
		}
	}
	var cards []trelloCard
	if err := t.do("/members/me/cards?filter=open", &cards); err != nil {
		return nil, err
	}
	var issues []Issue
	for _, c := range cards {
		if l, ok := lists[c.IDList]; ok {
			issues = append(issues, c.issue(l, names[c.IDBoard]))
		}
	}
	return issues, nil
}

func (t trello) Issue(key string) (Issue, error) {
	var c trelloCard
	if err := t.do("/cards/"+url.PathEscape(key)+"?list=true&board=true&board_fields=name&actions=commentCard", &c); err != nil {
		return Issue{}, err
	}
	var l trelloList
	if c.List != nil {
		l = *c.List
	}
	return c.issue(l, c.Board.Name), nil
}

func (t trello) Done(keys []string) ([]string, error) {
	var done []string
	for _, k := range keys {
		if !trelloID.MatchString(k) {
			continue
		}
		var c trelloCard
		err := t.do("/cards/"+k+"?fields=closed&list=true", &c)
		if notFound(err) || (err == nil && (c.Closed || (c.List != nil && trelloState(*c.List).Type == Completed))) {
			done = append(done, k)
		}
	}
	return done, nil
}

func (t trello) States(boardNames []string) ([]State, error) {
	boards, err := t.boards()
	if err != nil {
		return nil, err
	}
	var out []State
	for _, b := range boards {
		if slices.Contains(boardNames, b.Name) {
			for _, l := range b.Lists {
				out = append(out, trelloState(l))
			}
		}
	}
	return out, nil
}

func (t trello) Me() (string, error) {
	var me struct{ FullName string }
	err := t.do("/members/me?fields=fullName", &me)
	return me.FullName, err
}

var trelloKind = Kind{
	Key:     "trello",
	Name:    "Trello",
	About:   "trello.com, with an API key and a token",
	Team:    "Board",
	APIHint: `To read or change tickets, use the Trello REST API at https://api.trello.com/1 with the header Authorization: OAuth oauth_consumer_key="$TRELLO_API_KEY", oauth_token="$TRELLO_TOKEN". A ticket is a card and its key is the card's shortLink (GET /1/cards/<key>); moving it to another status means PUT /1/cards/<key>?idList=<list id>, with the lists from GET /1/boards/<idBoard>/lists.`,
	Fields: []Field{
		{Env: "TRELLO_API_KEY", Label: "API key", Hint: "trello.com/power-ups/admin → New (any name) → API key → Generate a new API key"},
		{Env: "TRELLO_TOKEN", Label: "Token", Hint: "on that same API key page, click the Token link and Allow", Secret: true},
	},
	Sources: []string{"assigned"},
	open: func(env map[string]string) backend {
		return trello{"https://api.trello.com/1", env["TRELLO_API_KEY"], env["TRELLO_TOKEN"]}
	},
}

type trello struct{ api, key, token string }

var (
	trelloID       = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)
	trelloDoneList = regexp.MustCompile(`(?i)\b(done|completed?|finished|conclu[íi]d[oa]s?|feitos?|finalizad[oa]s?)\b`)
)

type trelloList struct {
	ID, Name string
	Pos      float64
	Closed   bool
}

type trelloBoardInfo struct {
	ID, Name string
	Lists    []trelloList
}

type trelloCard struct {
	Name, Desc, ShortLink, ShortURL, IDBoard, IDList string
	DateLastActivity                                 time.Time
	Closed                                           bool
	Labels                                           []struct{ Name string }
	List                                             *trelloList
	Board                                            struct{ Name string }
	Actions                                          []struct {
		Date          time.Time
		Data          struct{ Text string }
		MemberCreator struct{ FullName string }
	}
}
