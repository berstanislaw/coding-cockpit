package config

import (
	"fmt"
	"kta/internal/shell"
	"kta/internal/tracker"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func init() { UseDir(WorkspaceDir(Workspace)) }

func UseDir(dir string) {
	ConfDir = dir
	EnvFile, SessionsFile, GroupsFile, SettingsFile = filepath.Join(dir, "env"), filepath.Join(dir, "sessions.json"), filepath.Join(dir, "groups.json"), filepath.Join(dir, "settings.json")
	StatusDir, RunDir, DailyDir, ReviewsDir = filepath.Join(dir, "status"), filepath.Join(dir, "run"), filepath.Join(dir, "daily"), filepath.Join(dir, "reviews")
}

func WorkspaceDir(name string) string {
	if name == "" {
		return RootDir
	}
	return filepath.Join(RootDir, "workspaces", name)
}

func ReadEnv(file string) map[string]string {
	env := map[string]string{}
	b, _ := os.ReadFile(file)
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		if k = strings.TrimSpace(k); ok && !strings.HasPrefix(k, "#") {
			env[k] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return env
}

func Load() (tracker.Client, error) {
	file := ReadEnv(EnvFile)
	env := func(k string) string { return file[k] }
	t, err := tracker.Open(env)
	for _, name := range tracker.EnvNames() {
		os.Unsetenv(name)
	}
	if err != nil {
		return tracker.Client{}, fmt.Errorf("%v · run %s", err, setupCmd)
	}
	if WorkDir = env("WORKSPACE_DIR"); WorkDir == "" {
		return tracker.Client{}, fmt.Errorf("workspace folder missing · run %s", setupCmd)
	}
	WorkDir = ExpandHome(WorkDir)
	if !IsDir(WorkDir) {
		return tracker.Client{}, fmt.Errorf("WORKSPACE_DIR %s is not a folder · run %s", WorkDir, setupCmd)
	}
	ClaudeSessionsDir = filepath.Join(Home, ".claude/projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(WorkDir, "-"))
	return t, nil
}

func ExpandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(Home, rest)
	}
	return path
}

func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func WriteEnv(env map[string]string) error {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(&b, "%s=%s\n", k, shell.Quote(env[k]))
	}
	if err := os.MkdirAll(ConfDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(EnvFile, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(EnvFile, 0o600)
}

var (
	Home, _    = os.UserHomeDir()
	SelfExe, _ = os.Executable()
	Workspace  = os.Getenv("KTA_WORKSPACE")
	setupCmd   = strings.Join(strings.Fields("./kta "+Workspace+" setup"), " ")

	ConfDir, EnvFile, SessionsFile, GroupsFile, SettingsFile, StatusDir, RunDir, DailyDir, ReviewsDir string
	WorkDir, ClaudeSessionsDir                                                                        string
)

var RootDir = filepath.Join(Home, ".config/kta")
