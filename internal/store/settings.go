package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"kta/internal/config"
	"kta/internal/tracker"
	"os"
	"slices"
)

func LoadSettings() (Settings, error) {
	var s Settings
	b, err := os.ReadFile(config.SettingsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		return s, fmt.Errorf("%s: %w", config.SettingsFile, err)
	}
	return s, nil
}

func (s Settings) Sources() []string {
	var out []string
	for _, src := range tracker.Sources {
		if slices.Contains(s.Tickets, src.Key) {
			out = append(out, src.Key)
		}
	}
	if len(out) == 0 {
		return []string{"assigned"}
	}
	return out
}

func (s Settings) Language() Language {
	for _, l := range Languages {
		if l.Code == s.DailyLanguage {
			return l
		}
	}
	return Languages[0]
}

type Settings struct {
	HiddenColumns    []string `json:"hiddenColumns,omitempty"`
	Tickets          []string `json:"tickets,omitempty"`
	DailyLanguage    string   `json:"dailyLanguage,omitempty"`
	NotificationsOff bool     `json:"notificationsOff,omitempty"`
	HiddenTabs       []string `json:"hiddenTabs,omitempty"`
	ReviewLabels     []string `json:"reviewLabels,omitempty"`
}

type Language struct {
	Code, Name, Prompt, Today, Examples string
	Weekdays                            []string
}

var Languages = []Language{
	{"pt", "português", "Brazilian Portuguese", "hoje", `"trabalhei no card que falava de importação de planilhas", "revisei um PR da tela de estoque", "investiguei um bug de permissão no catálogo"`, []string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}},
	{"en", "english", "English", "today", `"worked on the card about spreadsheet imports", "reviewed a PR for the stock screen", "looked into a permission bug in the catalog"`, []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}},
	{"es", "español", "Spanish", "hoy", `"trabajé en la tarjeta sobre importación de planillas", "revisé un PR de la pantalla de stock", "investigué un bug de permisos en el catálogo"`, []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}},
}
