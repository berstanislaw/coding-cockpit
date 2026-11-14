package ui

import (
	"kta/internal/github"
	"kta/internal/review"
	"kta/internal/store"
	"kta/internal/tracker"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func Run() error {
	sessions, groups, err := store.LoadState()
	if err != nil {
		return err
	}
	settings, err := store.LoadSettings()
	if err != nil {
		return err
	}
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "search"
	ta := textarea.New()
	ta.Placeholder = "what do you want in this session?"
	ta.ShowLineNumbers = false
	ask := textinput.New()
	ask.Prompt = ""
	pick := textinput.New()
	pick.Prompt = "> "
	pick.Placeholder = "type to filter, or a new name"
	m := model{sessions: sessions, groups: groups, settings: settings, live: map[string]string{}, status: map[string]string{}, search: search, ta: ta, vp: viewport.New(0, 0), loading: true, paneFocused: true,
		reviews: map[string]*review.Review{}, busy: map[string]bool{}, generating: map[string]bool{}, sending: map[string]bool{}, ask: ask, pick: pick, rvp: viewport.New(0, 0)}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithReportFocus()).Run()
	return err
}

type focus int

const (
	focusBoard focus = iota
	focusStrip
	focusNew
	focusAsk
)

type tab int

const (
	tabKanban tab = iota
	tabSessions
	tabReviews
	tabDaily
	tabSettings
)

var tabNames = []string{"kanban", "sessions", "reviews", "daily", "settings"}

type model struct {
	w, h        int
	tab         tab
	focus       focus
	issues      []tracker.Issue
	cols        []column
	col, row    int
	search      textinput.Model
	searching   bool
	sessions    map[string][]store.Session
	live        map[string]string
	status      map[string]string
	ticket      *tracker.Issue
	sel         int
	vp          viewport.Model
	mdRaw       string
	mdWidth     int
	ta          textarea.Model
	pending     string
	loading     bool
	paneFocused bool
	err         error
	linearPRs   map[string][]string
	daily       string
	dailyAt     time.Time
	dailyBusy   bool
	dailyVP     viewport.Model
	dailyW      int
	settings    store.Settings
	states      []tracker.State
	stateTeams  []string
	setSel      int
	showKeys    bool
	prs         []github.PR
	prSel       int
	openPR      *github.PR
	reviews     map[string]*review.Review
	thread      int
	ask         textinput.Model
	askKind     string
	busy        map[string]bool
	generating  map[string]bool
	sending     map[string]bool
	labelPool   []string
	prsLoading  bool
	prsAt       time.Time
	picking     bool
	pick        textinput.Model
	pickSel     int
	workspaces  []workspaceInfo
	rvp         viewport.Model
	rvpKey      string
	marked      []string
	newIn       string
	groups      []store.Group
	collapsed   map[string]cardGroup
	frame       int
	animOn      bool
}
