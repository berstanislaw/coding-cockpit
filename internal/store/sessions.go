package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"kta/internal/config"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

func TitleFor(key, name string) string {
	if key == FreeKey {
		return name
	}
	return key + " · " + name
}

func SessionTitle(sessions map[string][]Session, groups []Group, id string) string {
	for key, list := range sessions {
		for _, x := range list {
			if x.ID != id {
				continue
			}
			if g := GroupByID(groups, strings.TrimPrefix(key, GroupPrefix)); IsGroupKey(key) && g != nil {
				return TitleFor(g.Title(), x.Name)
			}
			return TitleFor(key, x.Name)
		}
	}
	return ""
}

func LoadSessions() (map[string][]Session, error) {
	m := map[string][]Session{}
	b, err := os.ReadFile(config.SessionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func locked(fn func() error) error {
	f, err := os.OpenFile(config.SessionsFile+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

func LoadState() (sessions map[string][]Session, groups []Group, err error) {
	err = locked(func() error {
		var err error
		if sessions, err = LoadSessions(); err != nil {
			return err
		}
		groups, err = LoadGroups()
		return err
	})
	return sessions, groups, err
}

func MutateState(change func(map[string][]Session, []Group) []Group) (sessions map[string][]Session, groups []Group, err error) {
	err = locked(func() error {
		var err error
		if sessions, err = LoadSessions(); err != nil {
			return err
		}
		if groups, err = LoadGroups(); err != nil {
			return err
		}
		if groups = change(sessions, groups); groups == nil {
			groups = []Group{}
		}
		if err := WriteJSON(config.GroupsFile, groups); err != nil {
			return err
		}
		return WriteJSON(config.SessionsFile, sessions)
	})
	return sessions, groups, err
}

func MutateSessions(change func(map[string][]Session)) (map[string][]Session, error) {
	sessions, _, err := MutateState(func(s map[string][]Session, gs []Group) []Group {
		change(s)
		return gs
	})
	return sessions, err
}

func WriteJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}

func NewUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func KeysOf(sessions map[string][]Session, id string) []string {
	var keys []string
	for key, list := range sessions {
		if slices.ContainsFunc(list, func(s Session) bool { return s.ID == id }) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

const FreeKey = "free"

type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}
