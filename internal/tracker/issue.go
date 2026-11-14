package tracker

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

type Issue struct {
	Key, Title, URL, Description string
	UpdatedAt                    time.Time
	Priority                     int
	State                        State
	Team                         string
	Labels                       []string
	Comments                     []Comment
	Links                        []string
}

type State struct {
	Name, Type string
	Position   float64
	Color      string
}

type Comment struct {
	Author, Body string
	CreatedAt    time.Time
}

const (
	Triage    = "triage"
	Backlog   = "backlog"
	Unstarted = "unstarted"
	Started   = "started"
	Completed = "completed"
	Canceled  = "canceled"
)

func Retryable(err error) bool {
	var u unavailable
	return errors.As(err, &u)
}

func send(name string, req *http.Request) (*http.Response, error) {
	res, err := httpClient.Do(req)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return nil, unavailable{fmt.Sprintf("%s didn't respond in %s", name, httpClient.Timeout), err}
	}
	if err != nil {
		return nil, unavailable{fmt.Sprintf("can't reach %s (%v)", name, errors.Unwrap(err)), err}
	}
	return res, nil
}

func notFound(err error) bool {
	var s statusError
	return errors.As(err, &s) && s.code == http.StatusNotFound
}

type unavailable struct {
	msg string
	err error
}

func (e unavailable) Error() string { return e.msg }

func (e unavailable) Unwrap() error { return e.err }

type statusError struct {
	code int
	msg  string
}

func (e statusError) Error() string { return e.msg }

var httpClient = &http.Client{Timeout: 20 * time.Second}
