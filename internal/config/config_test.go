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
