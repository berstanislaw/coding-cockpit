package tracker

import (
	"fmt"
	"slices"
	"strings"
)

type Kind struct {
	Key, Name, About, Team, APIHint string
	Fields                          []Field
	Sources                         []string
	open                            func(env map[string]string) backend
}

type Field struct {
	Env, Label, Hint, Placeholder string
	Secret                        bool
	clean                         func(string) string
}

type Client struct {
	Kind
	backend
}

type backend interface {
	Me() (string, error)
	Board(sources []string) ([]Issue, error)
	Issue(key string) (Issue, error)
	Done(keys []string) ([]string, error)
	States(teams []string) ([]State, error)
}

func (f Field) Clean(v string) string {
	if v = strings.TrimSpace(v); f.clean != nil {
		v = f.clean(v)
	}
	return v
}

func (k Kind) Supports(source string) bool {
	return k.Sources == nil || slices.Contains(k.Sources, source)
}

func Connect(k Kind, env func(string) string) (Client, error) {
	values := map[string]string{}
	for _, f := range k.Fields {
		if values[f.Env] = f.Clean(env(f.Env)); values[f.Env] == "" {
			return Client{}, fmt.Errorf("%s: %s is missing", k.Name, f.Label)
		}
	}
	return Client{k, k.open(values)}, nil
}

func Open(env func(string) string) (Client, error) {
	key := KeyOf(env)
	i := slices.IndexFunc(All, func(k Kind) bool { return k.Key == key })
	if i < 0 {
		var keys []string
		for _, k := range All {
			keys = append(keys, k.Key)
		}
		return Client{}, fmt.Errorf("TRACKER=%s isn't one of %s", key, strings.Join(keys, ", "))
	}
	return Connect(All[i], env)
}

func KeyOf(env func(string) string) string {
	if key := env("TRACKER"); key != "" {
		return key
	}
	if env("JIRA_URL") != "" {
		return "jira"
	}
	return "linear"
}

func Names() []string {
	out := make([]string, len(All))
	for i, k := range All {
		out[i] = k.Name
	}
	return out
}

func EnvNames() []string {
	var out []string
	for _, k := range All {
		for _, f := range k.Fields {
			out = append(out, f.Env)
		}
	}
	return out
}

var All = []Kind{linearKind, jiraKind, trelloKind}

var Sources = []struct{ Key, Label string }{
	{"assigned", "assigned to me"},
	{"created", "created by me"},
	{"subscribed", "subscribed to"},
}
