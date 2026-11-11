package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type PR struct {
	ID, Title, URL, Repo, Author, HeadSHA, CI, Decision string
	Number                                              int
	Mine                                                bool
	Threads                                             []Received
}

type Received struct {
	Path, Author, Body string
	Line               int
}

type Review struct {
	Title    string            `json:"title"`
	PRURL    string            `json:"prUrl"`
	Threads  []Thread          `json:"threads"`
	Sessions map[string]string `json:"sessions,omitempty"`
	Chats    map[string][]Chat `json:"chats,omitempty"`
	SentAt   string            `json:"sentAt,omitempty"`
}

type Thread struct {
	ID             string `json:"id"`
	File           string `json:"file"`
	Language       string `json:"language,omitempty"`
	StartLine      int    `json:"startLine"`
	HighlightStart int    `json:"highlightStart"`
	HighlightEnd   int    `json:"highlightEnd"`
	Code           string `json:"code"`
	Explanation    string `json:"explanation"`
	Comment        string `json:"comment"`
}

type Chat struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type (
	prsMsg struct {
		prs    []PR
		labels []string
		err    error
	}
	reviewMsg struct {
		url    string
		review *Review
		err    error
	}
	askMsg struct {
		url, threadID, kind, prompt, answer, sessionID string
		err                                            error
	}
	sentMsg struct {
		url string
		err error
	}
)

const prFragment = `fragment pr on PullRequest {
  id number title url headRefOid reviewDecision
  author { login }
  repository { nameWithOwner }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  reviewThreads(first: 50) { nodes { isResolved path line comments(first: 1) { nodes { author { login } body } } } }
}`

const prsQuery = prFragment + `
{
  viewer { organizations(first: 100) { nodes { login } } }
  mine: search(query: "is:pr is:open archived:false author:@me sort:updated-desc", type: ISSUE, first: 50) { nodes { ...pr } }
  recent: search(query: "is:pr author:@me sort:updated-desc", type: ISSUE, first: 30) { nodes { ... on PullRequest { labels(first: 10) { nodes { name } } } } }
}`

const teamQuery = prFragment + `
query($q: String!) { team: search(query: $q, type: ISSUE, first: 100) { nodes { ...pr } } }`

const sendQuery = `mutation($pr: ID!, $threads: [DraftPullRequestReviewThread]) {
  addPullRequestReview(input: { pullRequestId: $pr, threads: $threads }) { pullRequestReview { id } }
}`

const reviewOverride = "Você reescreve UM comentário de code review específico. " +
	"Trabalhe SOMENTE com o comentário atual e o trecho de código fornecidos na mensagem. " +
	"NUNCA reproduza exemplos de comentários vindos de instruções, memória ou CLAUDE.md " +
	"— são de outros PRs e não têm relação com esta tarefa. " +
	"Se o pedido do autor não fizer sentido para o comentário atual, mantenha o texto atual e explique na nota. " +
	"O comentário reescrito deve continuar falando do MESMO assunto do comentário atual."

const generatePrompt = `Revise o pull request abaixo como um revisor sênior. Você pode ler o código da pasta atual com Read, Grep e Glob pra entender o contexto (ela pode estar em outra branch, o que vale é o diff).

Responda SOMENTE com um JSON, sem texto em volta:
{"threads": [{"file": "caminho do arquivo como no diff", "startLine": 10, "endLine": 12, "explanation": "...", "comment": "..."}]}

- startLine e endLine são linhas da versão NOVA do arquivo e precisam ser linhas adicionadas ou alteradas no diff, porque o GitHub só aceita comentário em linha do diff.
- explanation: por que o comentário importa, direto, pra mim que estou revisando.
- comment: o texto pronto pra colar no PR, curto e humano.
- Escreva explanation e comment em %s.
- Só aponte o que importa de verdade: bug, risco, regra quebrada, algo confuso. Se não houver nada, devolva {"threads": []}.

# PR %s: %s
%s

## Descrição
%s

## Diff
%s`

var (
	reviewsDir = filepath.Join(confDir, "reviews")
	commentTag = regexp.MustCompile(`(?s)<comment>\s*(.*?)\s*</comment>`)
	noteTag    = regexp.MustCompile(`(?s)<note>\s*(.*?)\s*</note>`)
	codeTag    = regexp.MustCompile(`(?s)<code>(.*?)</code>`)
	codeFence  = regexp.MustCompile("(?s)^```[^\\n]*\\n(.*?)\\n```\\s*$")
	anyTag     = regexp.MustCompile(`</?(comment|note)>`)
	blankRuns  = regexp.MustCompile(`\n{3,}`)
	extLang    = map[string]string{".go": "go", ".ts": "typescript", ".tsx": "tsx", ".js": "javascript", ".jsx": "jsx", ".py": "python", ".sql": "sql", ".md": "markdown", ".mdx": "mdx", ".json": "json", ".yml": "yaml", ".yaml": "yaml", ".css": "css", ".sh": "bash"}
)

func run(dir string, stdin []byte, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, fmt.Errorf("%s took longer than %s", name, timeout)
	}
	if err != nil {
		return out, fmt.Errorf("%s: %s", name, strings.TrimSpace(stderr.String()+" "+string(out)))
	}
	return out, nil
}

func ghGraphQL(query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	b, err := run("", body, time.Minute, "gh", "api", "graphql", "--input", "-")
	var r struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if json.Unmarshal(b, &r) == nil && len(r.Errors) > 0 {
		return errors.New(r.Errors[0].Message)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(r.Data, out)
}

func prsCmd(labels []string) tea.Cmd {
	return func() tea.Msg {
		if _, err := exec.LookPath("gh"); err != nil {
			return prsMsg{err: errors.New("reviews need the GitHub CLI: install gh and run gh auth login")}
		}
		prs, pool, err := fetchPRs(labels)
		return prsMsg{prs, pool, err}
	}
}

func fetchPRs(labels []string) ([]PR, []string, error) {
	type node struct {
		ID, Title, URL, HeadRefOid, ReviewDecision string
		Number                                     int
		Author                                     struct{ Login string }
		Repository                                 struct{ NameWithOwner string }
		Commits                                    struct {
			Nodes []struct {
				Commit struct{ StatusCheckRollup *struct{ State string } }
			}
		}
		ReviewThreads struct {
			Nodes []struct {
				IsResolved bool
				Path       string
				Line       int
				Comments   struct {
					Nodes []struct {
						Author struct{ Login string }
						Body   string
					}
				}
			}
		}
		Labels struct{ Nodes []struct{ Name string } }
	}
	type search struct{ Nodes []node }
	var d struct {
		Viewer struct {
			Organizations struct{ Nodes []struct{ Login string } }
		}
		Mine, Team, Recent search
	}
	if err := ghGraphQL(prsQuery, nil, &d); err != nil {
		return nil, nil, err
	}
	if len(labels) > 0 && len(d.Viewer.Organizations.Nodes) > 0 {
		quoted := make([]string, len(labels))
		for i, l := range labels {
			quoted[i] = strconv.Quote(l)
		}
		q := "is:pr is:open draft:false archived:false -author:@me sort:updated-desc label:" + strings.Join(quoted, ",")
		for _, o := range d.Viewer.Organizations.Nodes {
			q += " org:" + o.Login
		}
		var t struct{ Team search }
		if err := ghGraphQL(teamQuery, map[string]any{"q": q}, &t); err != nil {
			return nil, nil, err
		}
		d.Team = t.Team
	}
	toPR := func(n node, mine bool) PR {
		p := PR{ID: n.ID, Title: n.Title, URL: n.URL, Repo: n.Repository.NameWithOwner, Author: n.Author.Login, HeadSHA: n.HeadRefOid, Decision: n.ReviewDecision, Number: n.Number, Mine: mine}
		if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
			p.CI = n.Commits.Nodes[0].Commit.StatusCheckRollup.State
		}
		for _, t := range n.ReviewThreads.Nodes {
			if !t.IsResolved && len(t.Comments.Nodes) > 0 {
				c := t.Comments.Nodes[0]
				p.Threads = append(p.Threads, Received{Path: t.Path, Line: t.Line, Author: c.Author.Login, Body: c.Body})
			}
		}
		return p
	}
	var prs []PR
	for _, n := range d.Mine.Nodes {
		prs = append(prs, toPR(n, true))
	}
	for _, n := range d.Team.Nodes {
		if n.URL != "" {
			prs = append(prs, toPR(n, false))
		}
	}
	var pool []string
	for _, n := range d.Recent.Nodes {
		for _, l := range n.Labels.Nodes {
			if !slices.Contains(pool, l.Name) {
				pool = append(pool, l.Name)
			}
		}
	}
	slices.Sort(pool)
	return prs, pool, nil
}

func prKey(p PR) string {
	return p.Repo[strings.LastIndex(p.Repo, "/")+1:] + "#" + strconv.Itoa(p.Number)
}

func isPRKey(key string) bool {
	return strings.Contains(key, "#")
}

func reviewFile(url string) string {
	m := prURL.FindStringSubmatch(url)
	if m == nil {
		return filepath.Join(reviewsDir, "unknown.json")
	}
	return filepath.Join(reviewsDir, m[1]+"-"+m[2]+"-"+m[3]+".json")
}

func loadReview(url string) (*Review, error) {
	b, err := os.ReadFile(reviewFile(url))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Review
	return &r, json.Unmarshal(b, &r)
}

func generateCmd(p PR, lang language) tea.Cmd {
	return func() tea.Msg {
		r, err := generateReview(p, lang)
		return reviewMsg{p.URL, r, err}
	}
}

func generateReview(p PR, lang language) (*Review, error) {
	diff, err := run("", nil, time.Minute, "gh", "pr", "diff", p.URL)
	if err != nil {
		return nil, err
	}
	body, _ := run("", nil, time.Minute, "gh", "pr", "view", p.URL, "--json", "body", "-q", ".body")
	if len(diff) > maxPromptBytes {
		diff = append([]byte(strings.ToValidUTF8(string(diff[:maxPromptBytes]), "")), "\n[diff cut here, too large]"...)
	}
	prompt := fmt.Sprintf(generatePrompt, lang.prompt, prKey(p), p.Title, p.URL, strings.TrimSpace(string(body)), diff)
	out, err := run(workDir, []byte(prompt), 15*time.Minute, "claude", "-p", "--output-format", "json", "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob")
	if err != nil {
		return nil, err
	}
	var res struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("bad claude output: %.200s", out)
	}
	var found struct {
		Threads []struct {
			File                 string
			StartLine, EndLine   int
			Explanation, Comment string
		}
	}
	start, end := strings.Index(res.Result, "{"), strings.LastIndex(res.Result, "}")
	if start < 0 || end < start || json.Unmarshal([]byte(res.Result[start:end+1]), &found) != nil {
		return nil, fmt.Errorf("claude didn't return the threads: %.300s", res.Result)
	}
	files := map[string][]string{}
	r := &Review{Title: fmt.Sprintf("PR #%d — %s", p.Number, p.Title), PRURL: p.URL, Sessions: map[string]string{"global:general": res.SessionID}}
	for i, t := range found.Threads {
		lines, ok := files[t.File]
		if !ok {
			raw, _ := run("", nil, time.Minute, "gh", "api", "-H", "Accept: application/vnd.github.raw", "repos/"+p.Repo+"/contents/"+t.File+"?ref="+p.HeadSHA)
			lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			files[t.File] = lines
		}
		hs := max(1, t.StartLine)
		he := max(hs, t.EndLine)
		from, to := max(1, hs-10), min(len(lines), he+10)
		code := ""
		if from <= to {
			code = strings.Join(lines[from-1:to], "\n")
		}
		r.Threads = append(r.Threads, Thread{ID: fmt.Sprintf("t%d", i+1), File: t.File, Language: extLang[filepath.Ext(t.File)], StartLine: from, HighlightStart: hs, HighlightEnd: he, Code: code, Explanation: t.Explanation, Comment: t.Comment})
	}
	return r, nil
}

func askPreamble(kind string, t *Thread) string {
	base := ""
	if t != nil {
		base = fmt.Sprintf("Contexto do trecho em revisão:\nArquivo: %s (linhas %d+)\n```%s\n%s\n```\n\nMinha explicação de por que o comentário importa:\n\"\"\"%s\"\"\"\n\nComentário sugerido atual:\n\"\"\"%s\"\"\"\n", t.File, t.StartLine, t.Language, t.Code, t.Explanation, t.Comment)
	}
	switch kind {
	case "explanation":
		return "Você é o mesmo revisor que escreveu a explicação abaixo. O autor (dono do PR) está conversando com você sobre ela: pode pedir esclarecimento, discordar ou querer aprender. Responda direto, defenda ou ceda com honestidade técnica. NÃO reescreva o comentário aqui, só discuta.\n\nSe (e SOMENTE se) o autor pedir para anotar/comentar/marcar no código, devolva o trecho ATUAL com comentários inline adicionados, dentro de <code>...</code>. Regras do <code>: reproduza o snippet inteiro na mesma linguagem, sem cercas markdown por dentro; NÃO acrescente nem remova linhas (preserve a contagem pra numeração/highlight não deslocar) — prefira comentário no fim da linha existente; comente só onde ajuda. Fora do <code>, escreva sua prosa normal. Se o autor não pediu para mexer no código, não emita <code>.\n\n" + base
	case "comment":
		return "Você é o revisor. O autor quer ajustar o COMENTÁRIO sugerido (tom, clareza, soar mais humano). Considere a instrução dele e devolva o comentário reescrito. Responda em duas partes exatas:\n<comment>\n(o comentário final, pronto pra colar no PR)\n</comment>\n<note>\n(1 linha curta sobre o que mudou)\n</note>\n\n" + base
	}
	return "Você é o revisor deste PR. O autor está te mandando um prompt geral sobre o review. Responda direto.\n" + base
}

func askCmd(r Review, threadID, kind, prompt string) tea.Cmd {
	var thread *Thread
	for i := range r.Threads {
		if r.Threads[i].ID == threadID {
			thread = &r.Threads[i]
		}
	}
	key := chatKey(threadID, kind)
	resume := r.Sessions[key]
	full := prompt
	switch {
	case resume == "":
		full = askPreamble(kind, thread) + "\n\n---\nAutor: " + prompt
	case kind == "comment" && thread != nil:
		full = fmt.Sprintf("Comentário atual (reescreva SOMENTE este texto, mesmo assunto):\n\"\"\"%s\"\"\"\n\n---\nAutor: %s", thread.Comment, prompt)
	case kind == "explanation" && thread != nil:
		full = fmt.Sprintf("Trecho atual (se for anotar, parta EXATAMENTE deste, mesmas linhas):\n```%s\n%s\n```\n\n---\nAutor: %s", thread.Language, thread.Code, prompt)
	}
	url := r.PRURL
	return func() tea.Msg {
		args := []string{"-p", full, "--output-format", "json", "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob", "--append-system-prompt", reviewOverride}
		if resume != "" {
			args = append(args, "--resume", resume)
		}
		out, err := run(workDir, nil, 3*time.Minute, "claude", args...)
		var res struct {
			Result    string `json:"result"`
			SessionID string `json:"session_id"`
		}
		if err == nil {
			err = json.Unmarshal(out, &res)
		}
		return askMsg{url: url, threadID: threadID, kind: kind, prompt: prompt, answer: res.Result, sessionID: res.SessionID, err: err}
	}
}

func chatKey(threadID, kind string) string {
	if threadID == "" {
		threadID = "global"
	}
	return threadID + ":" + kind
}

func applyAnswer(r *Review, msg askMsg) {
	if r.Sessions == nil {
		r.Sessions = map[string]string{}
	}
	if r.Chats == nil {
		r.Chats = map[string][]Chat{}
	}
	key := chatKey(msg.threadID, msg.kind)
	if msg.sessionID != "" {
		r.Sessions[key] = msg.sessionID
	}
	bubble := msg.answer
	if msg.err != nil {
		bubble = "error: " + msg.err.Error()
	}
	for i := range r.Threads {
		t := &r.Threads[i]
		if t.ID != msg.threadID || msg.err != nil {
			continue
		}
		if msg.kind == "comment" {
			bubble = "comment updated"
			if m := commentTag.FindStringSubmatch(msg.answer); m != nil {
				t.Comment = m[1]
			} else {
				bubble = strings.TrimSpace(anyTag.ReplaceAllString(msg.answer, ""))
			}
			if m := noteTag.FindStringSubmatch(msg.answer); m != nil {
				bubble = m[1]
			}
		}
		if m := codeTag.FindStringSubmatch(msg.answer); m != nil {
			code := strings.TrimRight(strings.TrimLeft(m[1], "\n"), " \t\n")
			if f := codeFence.FindStringSubmatch(code); f != nil {
				code = f[1]
			}
			t.Code = code
			if msg.kind != "comment" {
				bubble = strings.TrimSpace(blankRuns.ReplaceAllString(codeTag.ReplaceAllString(msg.answer, ""), "\n\n"))
				if bubble == "" {
					bubble = "comentei no código aqui do lado."
				}
			}
		}
	}
	r.Chats[key] = append(r.Chats[key], Chat{"you", msg.prompt}, Chat{"claude", bubble})
}

func sendCmd(p PR, r Review) tea.Cmd {
	threads := make([]map[string]any, len(r.Threads))
	for i, t := range r.Threads {
		th := map[string]any{"path": t.File, "line": t.HighlightEnd, "side": "RIGHT", "body": t.Comment}
		if t.HighlightStart < t.HighlightEnd {
			th["startLine"], th["startSide"] = t.HighlightStart, "RIGHT"
		}
		threads[i] = th
	}
	return func() tea.Msg {
		var out any
		return sentMsg{p.URL, ghGraphQL(sendQuery, map[string]any{"pr": p.ID, "threads": threads}, &out)}
	}
}

func prPrompt(p PR, request string, previous []string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		request = "Read the pull request below and tell me how you want to proceed."
	}
	body, _ := run("", nil, time.Minute, "gh", "pr", "view", p.URL, "--json", "body", "-q", ".body")
	parts := append([]string{request, ""}, previous...)
	parts = append(parts, fmt.Sprintf("# Pull request %s: %s", prKey(p), p.Title), p.URL, "", "## Description", strings.TrimSpace(string(body)))
	if len(p.Threads) > 0 {
		parts = append(parts, "", "## Open review threads")
		for _, t := range p.Threads {
			parts = append(parts, fmt.Sprintf("- %s:%d @%s: %s", t.Path, t.Line, t.Author, t.Body))
		}
	}
	return strings.Join(parts, "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func saveReview(r *Review) error {
	if err := os.MkdirAll(reviewsDir, 0o700); err != nil {
		return err
	}
	return writeJSON(reviewFile(r.PRURL), r)
}

func (m model) currentReview() *Review {
	if m.openPR == nil {
		return nil
	}
	return m.reviews[m.openPR.URL]
}

func (m model) curThread() *Thread {
	r := m.currentReview()
	if r == nil || len(r.Threads) == 0 {
		return nil
	}
	return &r.Threads[max(0, min(m.thread, len(r.Threads)-1))]
}

func (m model) prStatus(p PR) string {
	var parts []string
	switch p.CI {
	case "SUCCESS":
		parts = append(parts, stateStyle["working"].Render("✓ CI"))
	case "FAILURE", "ERROR":
		parts = append(parts, errStyle.Render("✗ CI"))
	case "PENDING", "EXPECTED":
		parts = append(parts, stateStyle["waiting"].Render("… CI"))
	}
	if p.Mine {
		switch p.Decision {
		case "APPROVED":
			parts = append(parts, stateStyle["working"].Render("approved"))
		case "CHANGES_REQUESTED":
			parts = append(parts, errStyle.Render("changes requested"))
		case "REVIEW_REQUIRED":
			parts = append(parts, dim.Render("review required"))
		}
		if n := len(p.Threads); n > 0 {
			parts = append(parts, stateStyle["waiting"].Render(fmt.Sprintf("%d open", n)))
		}
		return strings.Join(parts, dim.Render(" · "))
	}
	r := m.reviews[p.URL]
	switch {
	case m.generating[p.URL]:
		parts = append(parts, stateStyle["background"].Render("reviewing…"))
	case r != nil && r.SentAt != "":
		parts = append(parts, dim.Render("sent"))
	case r != nil:
		parts = append(parts, prStyle.Render(plural(len(r.Threads), "thread")))
	}
	return strings.Join(parts, dim.Render(" · "))
}

func (m model) prListView(height int) string {
	w := m.inner()
	if m.prs == nil {
		if m.prsLoading {
			return dim.Render("loading your pull requests…")
		}
		return dim.Render("press r to load your pull requests")
	}
	var lines []string
	at := 0
	row := func(i int, p PR) {
		cursor := "  "
		if i == m.prSel {
			cursor, at = lipgloss.NewStyle().Foreground(accent).Render("▸ "), len(lines)
		}
		left := cursor + prStyle.Render(prTag(p.URL)) + " " + p.Title
		if !p.Mine {
			left += dim.Render("  @" + p.Author)
		}
		if b := m.badge(prKey(p)); b != "" {
			left += " " + b
		}
		status := m.prStatus(p)
		left = ansi.Truncate(left, max(10, w-lipgloss.Width(status)-2), "…")
		lines = append(lines, left+strings.Repeat(" ", max(2, w-lipgloss.Width(left)-lipgloss.Width(status)))+status)
	}
	lines = append(lines, bold.Render("Mine"))
	team := 0
	for i, p := range m.prs {
		if p.Mine {
			row(i, p)
		} else {
			team++
		}
	}
	if len(m.prs) == team {
		lines = append(lines, dim.Render("  no open pull requests"))
	}
	head := bold.Render("Team")
	if len(m.settings.ReviewLabels) > 0 {
		head += dim.Render(" · " + strings.Join(m.settings.ReviewLabels, ", "))
	}
	lines = append(lines, "", head)
	switch {
	case len(m.settings.ReviewLabels) == 0:
		lines = append(lines, dim.Render("  pick your team's labels in settings (5)"))
	case team == 0:
		lines = append(lines, dim.Render("  no open pull requests with these labels"))
	}
	for i, p := range m.prs {
		if !p.Mine {
			row(i, p)
		}
	}
	return strings.Join(lines[max(0, at-height+2):], "\n")
}

func (m model) prPanelTop() string {
	w, p := m.inner(), m.openPR
	head := bold.Render(prKey(*p)) + dim.Render(" · ") + p.Title
	if !p.Mine {
		head += dim.Render("  @" + p.Author)
	}
	lines := []string{ansi.Truncate(head+"  "+m.prStatus(*p), w, "…")}
	if p.Mine {
		lines = append(lines, m.sessionLines()...)
	} else {
		lines = append(lines, m.reviewStatus())
	}
	return strings.Join(append(lines, dim.Render(strings.Repeat("─", w))), "\n")
}

func (m model) reviewStatus() string {
	p, r, t := m.openPR, m.currentReview(), m.curThread()
	switch {
	case m.pending == "r"+p.URL:
		return errStyle.Render("press r again to review it again (replaces these threads)")
	case m.pending == "s"+p.URL:
		return errStyle.Render(fmt.Sprintf("press s again to send %s to GitHub as a draft review", plural(len(r.Threads), "thread")))
	case t != nil && m.pending == "x"+t.ID:
		return errStyle.Render("press x again to discard this thread")
	case m.generating[p.URL]:
		return stateStyle["background"].Render("Claude is reviewing this PR, it can take a few minutes…")
	case m.sending[p.URL]:
		return stateStyle["background"].Render("sending the draft review…")
	case r == nil:
		return dim.Render("r to review this PR with Claude")
	case t == nil:
		return dim.Render("no threads · r r to review it again")
	}
	line := fmt.Sprintf("thread %d/%d · %s L%d", m.thread+1, len(r.Threads), t.File, t.HighlightStart)
	if t.HighlightEnd != t.HighlightStart {
		line += fmt.Sprintf("–%d", t.HighlightEnd)
	}
	if r.SentAt != "" {
		if at, err := time.Parse(time.RFC3339, r.SentAt); err == nil {
			line += dim.Render("  · sent as draft " + at.Local().Format("02/01 15:04"))
		}
	}
	return line
}

func (m model) prPanelBody() string {
	w, p := m.inner(), m.openPR
	wrap := func(s string) string { return ansi.Wordwrap(s, w-2, "") }
	if p.Mine {
		if len(p.Threads) == 0 {
			return dim.Render("no open review threads")
		}
		lines := []string{bold.Render(fmt.Sprintf("Open threads (%d)", len(p.Threads)))}
		for _, t := range p.Threads {
			lines = append(lines, "", dim.Render(fmt.Sprintf("%s:%d", t.Path, t.Line))+"  @"+t.Author, wrap(t.Body))
		}
		return strings.Join(lines, "\n")
	}
	t := m.curThread()
	if t == nil {
		return ""
	}
	var lines []string
	for i, l := range strings.Split(t.Code, "\n") {
		n, mark := t.StartLine+i, dim.Render("│")
		if n >= t.HighlightStart && n <= t.HighlightEnd {
			mark = lipgloss.NewStyle().Foreground(focusColor).Render("▌")
		}
		lines = append(lines, ansi.Truncate(dim.Render(fmt.Sprintf("%4d ", n))+mark+" "+strings.ReplaceAll(l, "\t", "  "), w, "…"))
	}
	chat := func(key string) {
		for _, c := range m.currentReview().Chats[key] {
			who := dim.Render("› you: ")
			if c.Role != "you" {
				who = lipgloss.NewStyle().Foreground(accent).Render("› claude: ")
			}
			lines = append(lines, wrap(who+c.Text))
		}
		if m.busy[p.URL+"|"+key] {
			lines = append(lines, lipgloss.NewStyle().Foreground(accent).Render("› claude: ")+dim.Render("thinking…"))
		}
	}
	lines = append(lines, "", bold.Render("Why"), wrap(t.Explanation))
	chat(chatKey(t.ID, "explanation"))
	lines = append(lines, "", bold.Render("Comment"), prStyle.Render(wrap(t.Comment)))
	chat(chatKey(t.ID, "comment"))
	if key := chatKey("", "general"); len(m.currentReview().Chats[key]) > 0 || m.busy[p.URL+"|"+key] {
		lines = append(lines, "", bold.Render("About the review"))
		chat(key)
	}
	return strings.Join(lines, "\n")
}

func (m model) askLine() string {
	if m.focus != focusAsk {
		return ""
	}
	label := map[string]string{"explanation": "ask about the why", "comment": "change the comment", "general": "ask about the review"}[m.askKind]
	return "\n" + lipgloss.NewStyle().Foreground(accent).Render(label+" › ") + m.ask.View()
}

func (m *model) fitReviews() {
	w := m.inner()
	m.ta.SetWidth(w)
	m.ta.SetHeight(5)
	m.ask.Width = max(10, w-25)
	if m.openPR == nil {
		return
	}
	askH := 0
	if m.focus == focusAsk {
		askH = 1
	}
	m.rvp.Width = w
	m.rvp.Height = max(1, m.stripHeight()-2-lipgloss.Height(m.prPanelTop())-askH)
	m.rvp.SetContent(m.prPanelBody())
	key := fmt.Sprintf("%s|%d|%d", m.openPR.URL, m.thread, w)
	if key != m.rvpKey {
		m.rvp.GotoTop()
		m.rvpKey = key
	}
}

func (m model) reviewsView() string {
	stripH := 0
	if m.openPR != nil {
		stripH = m.stripHeight()
	}
	listH := m.h - 2 - stripH
	body := m.box("reviews", m.focus == focusBoard, listH-2, m.prListView(listH-2))
	if m.openPR != nil {
		body += "\n" + m.box(prKey(*m.openPR), m.focus != focusBoard, stripH-2, m.prPanelTop()+"\n"+m.rvp.View()+m.askLine())
	}
	return body
}

func (m model) openPRPanel(p PR) model {
	m.openPR, m.thread, m.sel, m.focus = &p, 0, 0, focusStrip
	pane := ""
	if p.Mine {
		pane = m.bestPane(prKey(p))
	}
	var err error
	if pane != "" {
		err = showRight(pane)
	} else {
		err = hideRight()
	}
	if err != nil {
		m.err = err
	}
	return m
}

func (m model) reviewsKey(k tea.KeyMsg, confirm string) (model, tea.Cmd) {
	key := k.String()
	switch {
	case m.focus == focusAsk:
		switch key {
		case "esc":
			m.focus = focusStrip
			return m, nil
		case "enter":
			prompt, r := strings.TrimSpace(m.ask.Value()), m.currentReview()
			m.focus = focusStrip
			if prompt == "" || r == nil {
				return m, nil
			}
			threadID := ""
			if t := m.curThread(); m.askKind != "general" && t != nil {
				threadID = t.ID
			}
			m.busy[r.PRURL+"|"+chatKey(threadID, m.askKind)] = true
			return m, askCmd(*r, threadID, m.askKind, prompt)
		}
		var cmd tea.Cmd
		m.ask, cmd = m.ask.Update(k)
		return m, cmd
	case m.focus == focusBoard:
		return m.prListKey(key)
	case m.openPR != nil && m.openPR.Mine:
		if key == "o" {
			openURL(m.openPR.URL)
			return m, nil
		}
		return m.stripKey(key, confirm)
	case m.openPR != nil:
		return m.teamKey(key, confirm)
	}
	return m, nil
}

func (m model) prListKey(key string) (model, tea.Cmd) {
	var sel *PR
	if m.prSel < len(m.prs) {
		sel = &m.prs[m.prSel]
	}
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.prSel = max(0, m.prSel-1)
	case "down", "j":
		m.prSel = max(0, min(len(m.prs)-1, m.prSel+1))
	case "enter":
		if sel != nil {
			return m.openPRPanel(*sel), nil
		}
	case "o":
		if sel != nil {
			openURL(sel.URL)
		}
	case "r":
		m.prsLoading = true
		return m, prsCmd(m.settings.ReviewLabels)
	case "tab", "alt+down":
		if m.openPR != nil {
			m.focus = focusStrip
		}
	case "esc":
		if err := hideRight(); err != nil {
			m.err = err
		}
		m.openPR = nil
	}
	return m, nil
}

func (m model) teamKey(key, confirm string) (model, tea.Cmd) {
	p, r, t := *m.openPR, m.currentReview(), m.curThread()
	switch key {
	case "q":
		return m, tea.Quit
	case "esc", "tab", "alt+up":
		m.focus = focusBoard
	case "left", "h":
		m.thread = max(0, m.thread-1)
	case "right", "l":
		if r != nil {
			m.thread = max(0, min(len(r.Threads)-1, m.thread+1))
		}
	case "up", "k":
		m.rvp.LineUp(1)
	case "down", "j":
		m.rvp.LineDown(1)
	case "pgup", "ctrl+u":
		m.rvp.HalfPageUp()
	case "pgdown", "ctrl+d", " ":
		m.rvp.HalfPageDown()
	case "o":
		openURL(p.URL)
	case "r":
		if m.generating[p.URL] {
			break
		}
		if r != nil && confirm != "r"+p.URL {
			m.pending = "r" + p.URL
			break
		}
		m.generating[p.URL] = true
		return m, generateCmd(p, m.settings.language())
	case "a", "c", "g":
		kind := map[string]string{"a": "explanation", "c": "comment", "g": "general"}[key]
		if r == nil || (t == nil && kind != "general") {
			break
		}
		m.askKind, m.focus = kind, focusAsk
		m.ask.Reset()
		return m, m.ask.Focus()
	case "x":
		if t == nil {
			break
		}
		if confirm != "x"+t.ID {
			m.pending = "x" + t.ID
			break
		}
		r.Threads = slices.DeleteFunc(r.Threads, func(x Thread) bool { return x.ID == t.ID })
		if err := saveReview(r); err != nil {
			m.err = err
		}
		m.thread = max(0, min(m.thread, len(r.Threads)-1))
	case "s":
		if r == nil || len(r.Threads) == 0 || m.sending[p.URL] {
			break
		}
		if confirm != "s"+p.URL {
			m.pending = "s" + p.URL
			break
		}
		m.sending[p.URL] = true
		return m, sendCmd(p, *r)
	}
	return m, nil
}

func (m model) updateReviews(msg tea.Msg) model {
	switch msg := msg.(type) {
	case prsMsg:
		m.prsLoading = false
		if msg.err != nil {
			if m.tab == tabReviews {
				m.err = msg.err
			}
			return m
		}
		m.prs, m.labelPool = msg.prs, msg.labels
		if m.prs == nil {
			m.prs = []PR{}
		}
		for _, p := range m.prs {
			if _, ok := m.reviews[p.URL]; !ok && !p.Mine {
				if r, err := loadReview(p.URL); err == nil && r != nil {
					m.reviews[p.URL] = r
				}
			}
			if m.openPR != nil && p.URL == m.openPR.URL {
				cp := p
				m.openPR = &cp
			}
		}
		m.prSel = max(0, min(m.prSel, len(m.prs)-1))
	case reviewMsg:
		delete(m.generating, msg.url)
		if msg.err != nil {
			m.err = msg.err
			return m
		}
		if err := saveReview(msg.review); err != nil {
			m.err = err
			return m
		}
		m.reviews[msg.url] = msg.review
		if m.openPR != nil && m.openPR.URL == msg.url {
			m.thread = 0
		}
	case askMsg:
		delete(m.busy, msg.url+"|"+chatKey(msg.threadID, msg.kind))
		if r := m.reviews[msg.url]; r != nil {
			applyAnswer(r, msg)
			if err := saveReview(r); err != nil {
				m.err = err
			}
		}
		if msg.err != nil {
			m.err = msg.err
		}
	case sentMsg:
		delete(m.sending, msg.url)
		if msg.err != nil {
			m.err = msg.err
			return m
		}
		if r := m.reviews[msg.url]; r != nil {
			r.SentAt = time.Now().Format(time.RFC3339)
			if err := saveReview(r); err != nil {
				m.err = err
			}
		}
	}
	return m
}

func (m model) reviewKeys() []keyHelp {
	tabs := keyHelp{"alt+1…5", "switch tabs", false}
	quit := keyHelp{"q", "quit", false}
	switch {
	case m.focus == focusAsk:
		return []keyHelp{{"enter", "send", true}, {"esc", "cancel", true}}
	case m.focus == focusNew:
		return []keyHelp{{"ctrl+s", "start the session", true}, {"esc", "cancel", true}}
	case m.focus == focusBoard || m.openPR == nil:
		keys := []keyHelp{{"enter", "open", true}, {"o", "open in browser", true}, {"r", "refresh", true}, {"↑↓", "select", false}}
		if m.openPR != nil {
			keys = append(keys, keyHelp{"alt+↓ / tab", "PR panel", false}, keyHelp{"esc", "close the PR", false})
		}
		return append(keys, tabs, quit)
	case m.openPR.Mine:
		return []keyHelp{{"enter", "show", true}, {"n", "new session", true}, {"o", "open in browser", true}, {"↑↓", "select", false}, {"x x", "stop the session", false}, {"d d", "remove the session", false}, {"alt+↑ / esc", "back to the list", false}, {"alt+→", "go to the Claude session", false}, tabs, quit}
	}
	return []keyHelp{{"←→", "thread", true}, {"a", "ask about the why", true}, {"c", "change the comment", true}, {"s s", "send as draft", true}, {"r", "review with Claude (r r again)", false}, {"g", "ask about the whole review", false}, {"x x", "discard the thread", false}, {"o", "open in browser", false}, {"↑↓ pgup/pgdn", "scroll", false}, {"alt+↑ / esc", "back to the list", false}, tabs, quit}
}
