package review

import (
	"kta/internal/github"

	tea "github.com/charmbracelet/bubbletea"
)

func SendCmd(p github.PR, r Review) tea.Cmd {
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
		return SentMsg{p.URL, github.GraphQL(github.SendQuery, map[string]any{"pr": p.ID, "threads": threads}, &out)}
	}
}

type SentMsg struct {
	URL string
	Err error
}
