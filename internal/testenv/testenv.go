package testenv

import (
	"kta/internal/config"
	"os"
	"testing"
)

func UseTemp(t *testing.T) string {
	config.UseDir(t.TempDir())
	os.MkdirAll(config.StatusDir, 0o700)
	return config.StatusDir
}

func Main(m *testing.M) {
	dir, err := os.MkdirTemp("", "kta-test-")
	if err != nil {
		panic(err)
	}
	config.RootDir = dir
	config.UseDir(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
