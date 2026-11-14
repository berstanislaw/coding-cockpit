package main

import (
	"fmt"
	"kta/internal/claude"
	"kta/internal/config"
	"kta/internal/tmux"
	"kta/internal/tracker"
	"kta/internal/ui"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

func main() {
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	switch {
	case arg == "hook" && len(os.Args) == 4:
		claude.RunHook(os.Args[2], os.Args[3], os.Stdin)
		return
	case arg == "transcript" && len(os.Args) == 3:
		if err := claude.PrintTranscript(os.Args[2], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	err := config.Load()
	if arg == "setup" || (err != nil && arg == "check" && term.IsTerminal(int(os.Stdin.Fd()))) {
		err = ui.RunWizard()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch arg {
	case "group":
		err = runGroup(os.Getenv("KTA_SESSION"), os.Args[2:], tracker.FetchIssue, os.Stdout)
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
	if socket, _, _ := strings.Cut(os.Getenv("TMUX"), ","); tmux.SelfPane == "" || !strings.HasPrefix(filepath.Base(socket), "kta") {
		fmt.Fprintln(os.Stderr, "run it through ./kta (it needs its own tmux)")
		os.Exit(1)
	}
	tmux.Run("set-option", "-p", "-t", tmux.SelfPane, "@kta_workspace", config.Workspace)
	for _, d := range []string{config.StatusDir, config.RunDir, config.ReviewsDir} {
		os.MkdirAll(d, 0o700)
	}
	if err := ui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmux.Run("kill-session")
}
