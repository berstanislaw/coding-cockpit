package tmux

import (
	"fmt"
	"kta/internal/config"
	"kta/internal/shell"
	"os"
	"os/exec"
	"strings"
)

func PaneInView() bool {
	out, err := Run("display", "-p", "-t", os.Getenv("TMUX_PANE"), "#{window_active}#{m:*focused*,#{client_flags}}")
	return err == nil && out == "11"
}

func Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func LivePanes() (live, titles map[string]string, err error) {
	out, err := Run("list-panes", "-a", "-F", "#{pane_id}\t#{@kta_session}\t#{@kta_title}")
	if err != nil {
		return nil, nil, err
	}
	live, titles = map[string]string{}, map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 || f[1] == "" {
			continue
		}
		live[f[1]] = f[0]
		if len(f) == 3 {
			titles[f[1]] = f[2]
		}
	}
	return live, titles, nil
}

func SetTitle(pane, title string) error {
	_, err := Run("set-option", "-p", "-t", pane, "@kta_title", title)
	return err
}

func rightPane() string {
	out, err := Run("list-panes", "-t", SelfPane, "-F", "#{pane_id}")
	if err != nil {
		return ""
	}
	for _, p := range strings.Fields(out) {
		if p != SelfPane {
			return p
		}
	}
	return ""
}

func KeepLeft() {
	out, err := Run("display", "-p", "-t", SelfPane, "#{pane_index} #{window_id}")
	if idx, win, ok := strings.Cut(out, " "); err == nil && ok && idx != "0" {
		Run("swap-pane", "-d", "-s", SelfPane, "-t", win+".0")
	}
}

func HideRight() error {
	if cur := rightPane(); cur != "" {
		_, err := Run("break-pane", "-d", "-s", cur, "-n", "claude")
		return err
	}
	return nil
}

func ShowRight(pane string) error {
	if rightPane() == pane {
		return nil
	}
	if err := HideRight(); err != nil {
		return err
	}
	_, err := Run("join-pane", "-h", "-d", "-l", "50%", "-s", pane, "-t", SelfPane)
	return err
}

func SwitchWorkspace(name string) error {
	live, _, err := LivePanes()
	if err != nil {
		return err
	}
	for _, pane := range live {
		KillPane(pane)
	}
	bin := shell.Quote(config.SelfExe)
	_, err = Run("respawn-pane", "-k", "-t", SelfPane, "-e", "KTA_WORKSPACE="+name, "trap : INT; "+bin+" check || export KTA_WORKSPACE="+shell.Quote(config.Workspace)+"; exec "+bin)
	return err
}

func FocusPane(pane string) error {
	_, err := Run("select-pane", "-t", pane)
	return err
}

func KillPane(pane string) error {
	_, err := Run("kill-pane", "-t", pane)
	return err
}

var SelfPane = os.Getenv("TMUX_PANE")
