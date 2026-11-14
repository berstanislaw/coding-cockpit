package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

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

const freeKey = "free"

var tabNames = []string{"kanban", "sessions", "reviews", "daily", "settings"}

type model struct {
	w, h        int
	tab         tab
	focus       focus
	issues      []Issue
	cols        []column
	col, row    int
	search      textinput.Model
	searching   bool
	sessions    map[string][]Session
	live        map[string]string
	status      map[string]string
	ticket      *Issue
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
	settings    Settings
	states      []State
	stateTeams  []string
	setSel      int
	showKeys    bool
	prs         []PR
	prSel       int
	openPR      *PR
	reviews     map[string]*Review
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
	groups      []Group
	collapsed   map[string]cardGroup
	frame       int
	animOn      bool
}

func main() {
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	switch {
	case arg == "hook" && len(os.Args) == 4:
		runHook(os.Args[2], os.Args[3], os.Stdin)
		return
	case arg == "transcript" && len(os.Args) == 3:
		if err := printTranscript(os.Args[2], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	err := loadConfig()
	if arg == "setup" || (err != nil && arg == "check" && term.IsTerminal(int(os.Stdin.Fd()))) {
		err = runWizard()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch arg {
	case "group":
		err = runGroup(os.Getenv("KTA_SESSION"), os.Args[2:], fetchIssue, os.Stdout)
	case "groups":
		err = runGroups(os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch arg {
	case "", "check", "setup", "group", "groups":
	default:
		fmt.Fprintln(os.Stderr, "usage: kta [setup | check | group … | groups]  (start the board with ./kta)")
		os.Exit(1)
	}
	if arg != "" {
		return
	}
	if socket, _, _ := strings.Cut(os.Getenv("TMUX"), ","); selfPane == "" || !strings.HasPrefix(filepath.Base(socket), "kta") {
		fmt.Fprintln(os.Stderr, "run it through ./kta (it needs its own tmux)")
		os.Exit(1)
	}
	tmux("set-option", "-p", "-t", selfPane, "@kta_workspace", workspace)
	for _, d := range []string{statusDir, runDir, reviewsDir} {
		os.MkdirAll(d, 0o700)
	}
	sessions, err := loadSessions()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	groups, err := loadGroups()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	settings, err := loadSettings()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
		reviews: map[string]*Review{}, busy: map[string]bool{}, generating: map[string]bool{}, sending: map[string]bool{}, ask: ask, pick: pick, rvp: viewport.New(0, 0)}
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithReportFocus()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmux("kill-session")
}
