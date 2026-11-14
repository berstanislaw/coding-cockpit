package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	home, _    = os.UserHomeDir()
	selfExe, _ = os.Executable()
	workspace  = os.Getenv("KTA_WORKSPACE")
	setupCmd   = strings.Join(strings.Fields("./kta "+workspace+" setup"), " ")
	prURL      = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	selfPane   = os.Getenv("TMUX_PANE")

	confDir, envFile, sessionsFile, groupsFile, settingsFile, statusDir, runDir, dailyDir, reviewsDir string
	workDir, claudeSessionsDir                                                                        string
)

func init() { useConfDir(workspaceDir(workspace)) }

var rootDir = filepath.Join(home, ".config/kta")

func useConfDir(dir string) {
	confDir = dir
	envFile, sessionsFile, groupsFile, settingsFile = filepath.Join(dir, "env"), filepath.Join(dir, "sessions.json"), filepath.Join(dir, "groups.json"), filepath.Join(dir, "settings.json")
	statusDir, runDir, dailyDir, reviewsDir = filepath.Join(dir, "status"), filepath.Join(dir, "run"), filepath.Join(dir, "daily"), filepath.Join(dir, "reviews")
}

func workspaceDir(name string) string {
	if name == "" {
		return rootDir
	}
	return filepath.Join(rootDir, "workspaces", name)
}

func readEnv(file string) map[string]string {
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

func loadConfig() error {
	env := readEnv(envFile)
	for k, v := range env {
		if v != "" {
			os.Setenv(k, v)
		}
	}
	tracker = env["TRACKER"]
	if tracker == "" {
		tracker = "linear"
		if env["JIRA_URL"] != "" {
			tracker = "jira"
		}
	}
	switch tracker {
	case "linear":
		if apiKey = os.Getenv("LINEAR_API_KEY"); apiKey == "" {
			return fmt.Errorf("Linear key missing · run %s", setupCmd)
		}
	case "jira":
		jiraURL, jiraEmail, jiraToken = jiraSite(os.Getenv("JIRA_URL")), os.Getenv("JIRA_EMAIL"), os.Getenv("JIRA_API_TOKEN")
		if jiraURL == "" || jiraEmail == "" || jiraToken == "" {
			return fmt.Errorf("Jira account missing · run %s", setupCmd)
		}
	default:
		return fmt.Errorf("TRACKER=%s in %s isn't linear or jira · run %s", tracker, envFile, setupCmd)
	}
	if workDir = os.Getenv("WORKSPACE_DIR"); workDir == "" {
		return fmt.Errorf("workspace folder missing · run %s", setupCmd)
	}
	workDir = expandHome(workDir)
	if !isDir(workDir) {
		return fmt.Errorf("WORKSPACE_DIR %s is not a folder · run %s", workDir, setupCmd)
	}
	claudeSessionsDir = filepath.Join(home, ".claude/projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(workDir, "-"))
	return nil
}

func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
