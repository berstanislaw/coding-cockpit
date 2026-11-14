package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"kta/internal/config"
	"os"
	"path/filepath"
	"strings"
)

func ContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "<") {
			return ""
		}
		return s
	}
	var blocks []struct{ Type, Text string }
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if t := strings.TrimSpace(b.Text); b.Type == "text" && t != "" && !strings.HasPrefix(t, "<") {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

func PrintTranscript(file string, w io.Writer) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var l struct {
			Type    string
			Message struct{ Content json.RawMessage }
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "assistant" {
			if text := ContentText(l.Message.Content); text != "" {
				fmt.Fprintf(w, "%s\n\n", text)
			}
		}
		if err != nil {
			return nil
		}
	}
}

func ReadStatus() map[string]string {
	status := map[string]string{}
	entries, _ := os.ReadDir(config.StatusDir)
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join(config.StatusDir, e.Name())); err == nil {
			status[e.Name()] = strings.TrimSpace(string(b))
		}
	}
	return status
}
