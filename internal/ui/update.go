package ui

import (
	"kta/internal/claude"
	"kta/internal/daily"
	"kta/internal/github"
	"kta/internal/review"
	"kta/internal/store"
	"kta/internal/tmux"
	"kta/internal/tracker"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func loadBoardCmd(sources []string) tea.Cmd {
	return func() tea.Msg { issues, err := tracker.FetchBoard(sources); return boardMsg{issues, err} }
}

func loadDetailCmd(key string) tea.Cmd {
	return func() tea.Msg { issue, err := tracker.FetchIssue(key); return detailMsg{issue, err} }
}

func refreshStateCmd() tea.Cmd {
	return func() tea.Msg {
		tmux.KeepLeft()
		live, titles, err := tmux.LivePanes()
		if err != nil {
			return stateMsg{err: err}
		}
		sessions, groups, err := store.LoadState()
		return stateMsg{live, claude.ReadStatus(), titles, sessions, groups, err}
	}
}

func doneCmd(keys []string) tea.Cmd {
	return func() tea.Msg {
		done, err := tracker.FetchDone(keys)
		if err != nil || len(done) == 0 {
			return nil
		}
		return doneMsg(done)
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func boardTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return boardTickMsg{} })
}

func resumeCmd(id, title string, extra []string) tea.Cmd {
	return func() tea.Msg {
		pane, err := claude.Launch(id, title, append([]string{"--resume", id}, extra...)...)
		return launchedMsg{id: id, pane: pane, err: err}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadBoardCmd(m.settings.Sources()), refreshStateCmd(), tick(), boardTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.FocusMsg:
		m.paneFocused = true
	case tea.BlurMsg:
		m.paneFocused = false
	case boardTickMsg:
		cmds := []tea.Cmd{loadBoardCmd(m.settings.Sources()), boardTick()}
		if m.tab == tabReviews && time.Since(m.prsAt) > 5*time.Minute {
			cmds = append(cmds, github.PRsCmd(m.settings.ReviewLabels))
		}
		return m, tea.Batch(cmds...)
	case boardMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.issues = msg.issues
			m.linearPRs = map[string][]string{}
			for _, i := range msg.issues {
				if prs := issuePRs(i); len(prs) > 0 {
					m.linearPRs[i.Identifier] = prs
				}
			}
			m.buildColumns()
			for _, i := range msg.issues {
				if m.ticket != nil && i.Identifier == m.ticket.Identifier {
					m.ticket.State, m.ticket.Title, m.ticket.Priority = i.State, i.Title, i.Priority
				}
			}
			var gone []string
			onBoard := func(key string) bool {
				return slices.ContainsFunc(msg.issues, func(i tracker.Issue) bool { return i.Identifier == key })
			}
			for key := range m.sessions {
				if store.IsTicketKey(key) && !onBoard(key) {
					gone = append(gone, key)
				}
			}
			for _, g := range m.groups {
				for _, t := range g.Tickets {
					if !onBoard(t) && !slices.Contains(gone, t) {
						gone = append(gone, t)
					}
				}
			}
			var cmds []tea.Cmd
			if len(gone) > 0 {
				cmds = append(cmds, doneCmd(gone))
			}
			var finished []string
			for _, i := range msg.issues {
				if t := i.State.Type; (t == "completed" || t == "canceled") && m.leftovers(i.Identifier) {
					finished = append(finished, i.Identifier)
				}
			}
			if len(finished) > 0 {
				m.forget(finished)
			}
			if teams := teamKeys(msg.issues); !slices.Equal(teams, m.stateTeams) {
				m.stateTeams = teams
				cmds = append(cmds, statesCmd(msg.issues))
			}
			cmd = tea.Batch(cmds...)
		}
	case github.PRsMsg, review.GeneratedMsg, review.AnswerMsg, review.SentMsg:
		m = m.updateReviews(msg)
	case doneMsg:
		m.forget(msg)
	case statesMsg:
		m.states = msg
		m.buildColumns()
	case detailMsg:
		if msg.err != nil {
			m.err = msg.err
			break
		}
		m.err = nil
		if m.ticket != nil && m.ticket.Identifier == msg.issue.Identifier {
			m.ticket = &msg.issue
			m.mdRaw, m.mdWidth = issueMarkdown(msg.issue), 0
		}
	case stateMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.live, m.status, m.sessions, m.groups = msg.live, msg.status, msg.sessions, msg.groups
			m.cleanupGroups()
			m.buildColumns()
			for id, pane := range msg.live {
				if title := m.titleOf(id); title != "" && title != msg.titles[id] {
					tmux.SetTitle(pane, title)
				}
			}
		}
	case animMsg:
		m.frame++
		if m.animating() {
			return m, animTick()
		}
		m.animOn = false
		return m, nil
	case tickMsg:
		return m, tea.Batch(refreshStateCmd(), tick())
	case launchedMsg:
		if msg.err != nil {
			m.err = msg.err
			break
		}
		if msg.rec != nil {
			sessions, groups, err := store.MutateState(func(s map[string][]store.Session, gs []store.Group) []store.Group {
				if msg.group != nil {
					gs = store.PlaceTickets(s, append(gs, *msg.group), msg.group.ID, msg.group.Tickets)
				}
				for _, key := range msg.keys {
					s[key] = append(s[key], *msg.rec)
				}
				return gs
			})
			if err != nil {
				m.err = err
				break
			}
			m.sessions, m.groups = sessions, groups
		}
		m.live[msg.id] = msg.pane
		m.selectSession(msg.id)
		m.focus = focusStrip
		if m.tab == tabKanban {
			m.focus = focusBoard
		}
		if err := tmux.ShowRight(msg.pane); err != nil {
			m.err = err
		} else {
			tmux.FocusPane(msg.pane)
		}
	case daily.Msg:
		m.dailyBusy = false
		if msg.Err != nil {
			m.err = msg.Err
			break
		}
		m.daily, m.dailyAt, m.dailyW = msg.Text, msg.At, 0
	case tea.KeyMsg:
		m, cmd = m.handleKey(msg)
	}
	switch {
	case m.tab == tabKanban && m.ticket != nil:
		m.fitViewport()
	case m.tab == tabDaily:
		m.fitDaily()
	case m.tab == tabReviews:
		m.fitReviews()
	default:
		m.ta.SetWidth(m.inner())
		m.ta.SetHeight(5)
	}
	if !m.animOn && m.animating() {
		m.animOn = true
		cmd = tea.Batch(cmd, animTick())
	}
	return m, cmd
}

func animTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

func (m model) animating() bool {
	return m.tab == tabKanban && len(m.collapsed) > 0
}

type (
	boardMsg struct {
		issues []tracker.Issue
		err    error
	}
	detailMsg struct {
		issue tracker.Issue
		err   error
	}
	stateMsg struct {
		live, status map[string]string
		titles       map[string]string
		sessions     map[string][]store.Session
		groups       []store.Group
		err          error
	}
	launchedMsg struct {
		keys     []string
		group    *store.Group
		id, pane string
		rec      *store.Session
		err      error
	}
	doneMsg      []string
	tickMsg      struct{}
	animMsg      struct{}
	boardTickMsg struct{}
)
