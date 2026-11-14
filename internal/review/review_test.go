package review

import (
	"kta/internal/github"
	"kta/internal/testenv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestApplyAnswer(t *testing.T) {
	r := &Review{Threads: []Thread{{ID: "t1", Code: "a\nb", Comment: "old"}}}
	ApplyAnswer(r, AnswerMsg{ThreadID: "t1", Kind: "comment", prompt: "shorter", answer: "<comment>\nnew text\n</comment>\n<note>\nmade it short\n</note>", SessionID: "s1"})
	if r.Threads[0].Comment != "new text" || r.Sessions["t1:comment"] != "s1" {
		t.Errorf("comment rewrite not applied: %+v %v", r.Threads[0], r.Sessions)
	}
	if got := r.Chats["t1:comment"]; len(got) != 2 || got[1].Text != "made it short" {
		t.Errorf("comment chat = %+v", got)
	}
	ApplyAnswer(r, AnswerMsg{ThreadID: "t1", Kind: "explanation", prompt: "annotate", answer: "done\n<code>\n```ts\na // here\nb\n```\n</code>"})
	if r.Threads[0].Code != "a // here\nb" || r.Chats["t1:explanation"][1].Text != "done" {
		t.Errorf("code annotation: %q / %+v", r.Threads[0].Code, r.Chats["t1:explanation"])
	}
	ApplyAnswer(r, AnswerMsg{ThreadID: "t1", Kind: "comment", prompt: "x", Err: os.ErrDeadlineExceeded})
	if r.Threads[0].Comment != "new text" || !strings.HasPrefix(r.Chats["t1:comment"][3].Text, "error: ") {
		t.Errorf("a failed call must not touch the comment: %+v", r.Chats["t1:comment"])
	}
}

func TestPRNames(t *testing.T) {
	p := github.PR{Repo: "acme/shop-web", Number: 1987}
	if k := github.PRKey(p); k != "shop-web#1987" || !github.IsPRKey(k) || github.IsPRKey("KAN-12") {
		t.Errorf("prKey = %s", k)
	}
	if f := filepath.Base(File("https://github.com/acme/shop-web/pull/1987")); f != "acme-shop-web-1987.json" {
		t.Errorf("reviewFile = %s", f)
	}
}

func TestDiffHunks(t *testing.T) {
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,4 @@ func main() {\n a\n+b\n+++ not a header\n@@ -10 +11 @@\n+c\ndiff --git a/y.go b/y.go\n--- a/y.go\n+++ b/y.go\n@@ -5,2 +6,3 @@\n+d\n"
	got := diffHunks(diff)
	if !slices.Equal(got["x.go"], [][2]int{{1, 4}, {11, 11}}) || !slices.Equal(got["y.go"], [][2]int{{6, 8}}) || len(got) != 2 {
		t.Errorf("diffHunks = %v", got)
	}
}

func TestMain(m *testing.M) { testenv.Main(m) }
