package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"kta/internal/claude"
	"kta/internal/config"
	"kta/internal/github"
	"kta/internal/shell"
	"kta/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func File(url string) string {
	m := github.URLPattern.FindStringSubmatch(url)
	if m == nil {
		return filepath.Join(config.ReviewsDir, "unknown.json")
	}
	return filepath.Join(config.ReviewsDir, m[1]+"-"+m[2]+"-"+m[3]+".json")
}

func Load(url string) (*Review, error) {
	b, err := os.ReadFile(File(url))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Review
	return &r, json.Unmarshal(b, &r)
}

func Save(r *Review) error {
	if err := os.MkdirAll(config.ReviewsDir, 0o700); err != nil {
		return err
	}
	return store.WriteJSON(File(r.PRURL), r)
}

func GenerateCmd(p github.PR, lang store.Language) tea.Cmd {
	return func() tea.Msg {
		r, err := generateReview(p, lang)
		return GeneratedMsg{p.URL, r, err}
	}
}

func generateReview(p github.PR, lang store.Language) (*Review, error) {
	diff, err := shell.Run("", nil, time.Minute, "gh", "pr", "diff", p.URL)
	if err != nil {
		return nil, err
	}
	body, _ := shell.Run("", nil, time.Minute, "gh", "pr", "view", p.URL, "--json", "body", "-q", ".body")
	hunks := diffHunks(string(diff))
	if len(diff) > claude.MaxPromptBytes {
		diff = append([]byte(strings.ToValidUTF8(string(diff[:claude.MaxPromptBytes]), "")), "\n[diff cut here, too large]"...)
	}
	prompt := fmt.Sprintf(generatePrompt, lang.Prompt, github.PRKey(p), p.Title, p.URL, strings.TrimSpace(string(body)), diff)
	out, err := shell.Run(config.WorkDir, []byte(prompt), 15*time.Minute, "claude", "-p", "--output-format", "json", "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob")
	if err != nil {
		return nil, err
	}
	var res claudeResult
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
	r := &Review{PRURL: p.URL, Sessions: map[string]string{"global:general": res.SessionID}}
	for i, t := range found.Threads {
		hs := max(1, t.StartLine)
		he := max(hs, t.EndLine)
		if !slices.ContainsFunc(hunks[t.File], func(h [2]int) bool { return hs >= h[0] && he <= h[1] }) {
			r.Skipped++
			continue
		}
		lines, ok := files[t.File]
		if !ok {
			raw, err := shell.Run("", nil, time.Minute, "gh", "api", "-H", "Accept: application/vnd.github.raw", "repos/"+p.Repo+"/contents/"+t.File+"?ref="+p.HeadSHA)
			if err != nil {
				raw = nil
			}
			lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			files[t.File] = lines
		}
		from, to := max(1, hs-10), min(len(lines), he+10)
		code := ""
		if from <= to {
			code = strings.Join(lines[from-1:to], "\n")
		}
		r.Threads = append(r.Threads, Thread{ID: fmt.Sprintf("t%d", i+1), File: t.File, Language: extLang[filepath.Ext(t.File)], StartLine: from, HighlightStart: hs, HighlightEnd: he, Code: code, Explanation: t.Explanation, Comment: t.Comment})
	}
	return r, nil
}

func diffHunks(diff string) map[string][][2]int {
	hunks, file, prev := map[string][][2]int{}, "", ""
	for _, line := range strings.Split(diff, "\n") {
		if f, ok := strings.CutPrefix(line, "+++ "); ok && strings.HasPrefix(prev, "--- ") {
			file = strings.TrimPrefix(f, "b/")
		} else if h := hunkHeader.FindStringSubmatch(line); h != nil {
			start, _ := strconv.Atoi(h[1])
			n := 1
			if h[2] != "" {
				n, _ = strconv.Atoi(h[2])
			}
			hunks[file] = append(hunks[file], [2]int{start, start + n - 1})
		}
		prev = line
	}
	return hunks
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

func AskCmd(r Review, threadID, kind, prompt string) tea.Cmd {
	var thread *Thread
	for i := range r.Threads {
		if r.Threads[i].ID == threadID {
			thread = &r.Threads[i]
		}
	}
	key := ChatKey(threadID, kind)
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
		args := []string{"-p", "--output-format", "json", "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob", "--append-system-prompt", reviewOverride}
		if resume != "" {
			args = append(args, "--resume", resume)
		}
		out, err := shell.Run(config.WorkDir, nil, 3*time.Minute, "claude", append(args, "--", full)...)
		var res claudeResult
		if err == nil {
			err = json.Unmarshal(out, &res)
		}
		return AnswerMsg{URL: url, ThreadID: threadID, Kind: kind, prompt: prompt, answer: res.Result, SessionID: res.SessionID, Err: err}
	}
}

func ChatKey(threadID, kind string) string {
	if threadID == "" {
		threadID = "global"
	}
	return threadID + ":" + kind
}

func ApplyAnswer(r *Review, msg AnswerMsg) {
	if r.Sessions == nil {
		r.Sessions = map[string]string{}
	}
	if r.Chats == nil {
		r.Chats = map[string][]Chat{}
	}
	key := ChatKey(msg.ThreadID, msg.Kind)
	if msg.SessionID != "" {
		r.Sessions[key] = msg.SessionID
	}
	bubble := msg.answer
	if msg.Err != nil {
		bubble = "error: " + msg.Err.Error()
	}
	for i := range r.Threads {
		t := &r.Threads[i]
		if t.ID != msg.ThreadID || msg.Err != nil {
			continue
		}
		if msg.Kind == "comment" {
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
			if msg.Kind != "comment" {
				bubble = strings.TrimSpace(blankRuns.ReplaceAllString(codeTag.ReplaceAllString(msg.answer, ""), "\n\n"))
				if bubble == "" {
					bubble = "comentei no código aqui do lado."
				}
			}
		}
	}
	r.Chats[key] = append(r.Chats[key], Chat{"you", msg.prompt}, Chat{"claude", bubble})
}

type claudeResult struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

type Review struct {
	PRURL    string            `json:"prUrl"`
	Threads  []Thread          `json:"threads"`
	Skipped  int               `json:"skipped,omitempty"`
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
	hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)
	commentTag = regexp.MustCompile(`(?s)<comment>\s*(.*?)\s*</comment>`)
	noteTag    = regexp.MustCompile(`(?s)<note>\s*(.*?)\s*</note>`)
	codeTag    = regexp.MustCompile(`(?s)<code>(.*?)</code>`)
	codeFence  = regexp.MustCompile("(?s)^```[^\\n]*\\n(.*?)\\n```\\s*$")
	anyTag     = regexp.MustCompile(`</?(comment|note)>`)
	blankRuns  = regexp.MustCompile(`\n{3,}`)
	extLang    = map[string]string{".go": "go", ".ts": "typescript", ".tsx": "tsx", ".js": "javascript", ".jsx": "jsx", ".py": "python", ".sql": "sql", ".md": "markdown", ".mdx": "mdx", ".json": "json", ".yml": "yaml", ".yaml": "yaml", ".css": "css", ".sh": "bash"}
)

type GeneratedMsg struct {
	URL    string
	Review *Review
	Err    error
}

type AnswerMsg struct {
	URL, ThreadID, Kind, prompt, answer, SessionID string
	Err                                            error
}
