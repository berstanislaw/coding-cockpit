package tracker

import (
	"net/http"
	"time"
)

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

var httpClient = &http.Client{Timeout: 20 * time.Second}
