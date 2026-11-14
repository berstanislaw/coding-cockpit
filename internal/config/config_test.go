package config

import (
	"os"
	"testing"
)

func TestReadEnv(t *testing.T) {
	UseDir(t.TempDir())
	os.WriteFile(EnvFile, []byte("# comment\nexport LINEAR_API_KEY=\"lin_api_x\"\nWORKSPACE_DIR = '~/work'\nbroken line\n"), 0o600)
	env := ReadEnv(EnvFile)
	if env["LINEAR_API_KEY"] != "lin_api_x" || env["WORKSPACE_DIR"] != "~/work" || len(env) != 2 {
		t.Errorf("readEnv = %v", env)
	}
}

func TestLoadReadsOnlyTheFile(t *testing.T) {
	UseDir(t.TempDir())
	work := t.TempDir()
	os.WriteFile(EnvFile, []byte("LINEAR_API_KEY='lin_api_x'\nWORKSPACE_DIR='"+work+"'\n"), 0o600)
	t.Setenv("JIRA_URL", "acme")
	t.Setenv("WORKSPACE_DIR", "/nowhere")
	got, err := Load()
	if err != nil || got.Key != "linear" || WorkDir != work {
		t.Errorf("variables in the shell must not change the workspace: %v, tracker %q, folder %q", err, got.Key, WorkDir)
	}
	if os.Getenv("JIRA_URL") != "" {
		t.Error("tracker variables are cleared from the environment")
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kta-test-")
	if err != nil {
		panic(err)
	}
	RootDir = dir
	UseDir(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
