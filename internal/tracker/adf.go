package tracker

import (
	"encoding/json"
	"fmt"
	"strings"
)

func adfMarkdown(raw json.RawMessage) string {
	var doc adfNode
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return strings.TrimSpace(adfBlocks(doc.Content))
}

func adfBlocks(nodes []adfNode) string { return adfJoin(nodes, "\n\n") }

func adfJoin(nodes []adfNode, sep string) string {
	var out []string
	for _, n := range nodes {
		if s := adfBlock(n); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}

func adfBlock(n adfNode) string {
	switch n.Type {
	case "paragraph":
		return adfInline(n.Content)
	case "heading":
		level, _ := n.Attrs["level"].(float64)
		return strings.Repeat("#", max(1, int(level))) + " " + adfInline(n.Content)
	case "bulletList", "orderedList":
		items := make([]string, len(n.Content))
		for k, item := range n.Content {
			marker := "- "
			if n.Type == "orderedList" {
				marker = fmt.Sprintf("%d. ", k+1)
			}
			items[k] = marker + strings.ReplaceAll(adfJoin(item.Content, "\n"), "\n", "\n"+strings.Repeat(" ", len(marker)))
		}
		return strings.Join(items, "\n")
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		return "```" + lang + "\n" + adfInline(n.Content) + "\n```"
	case "blockquote", "panel":
		return "> " + strings.ReplaceAll(adfBlocks(n.Content), "\n", "\n> ")
	case "rule":
		return "---"
	case "table":
		var rows []string
		for _, row := range n.Content {
			cells := make([]string, len(row.Content))
			for k, cell := range row.Content {
				cells[k] = strings.ReplaceAll(adfBlocks(cell.Content), "\n", " ")
			}
			rows = append(rows, "| "+strings.Join(cells, " | ")+" |")
			if len(rows) == 1 {
				rows = append(rows, "|"+strings.Repeat(" --- |", len(cells)))
			}
		}
		return strings.Join(rows, "\n")
	case "mediaSingle", "mediaGroup", "media":
		return "[attachment]"
	case "text", "hardBreak", "mention", "emoji", "status", "inlineCard":
		return adfInline([]adfNode{n})
	}
	return adfBlocks(n.Content)
}

func adfInline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			t := n.Text
			for _, mk := range n.Marks {
				switch mk.Type {
				case "strong":
					t = "**" + t + "**"
				case "em":
					t = "*" + t + "*"
				case "code":
					t = "`" + t + "`"
				case "strike":
					t = "~~" + t + "~~"
				case "link":
					if href, _ := mk.Attrs["href"].(string); href != "" {
						t = "[" + t + "](" + href + ")"
					}
				}
			}
			b.WriteString(t)
		case "hardBreak":
			b.WriteString("\n")
		case "mention", "emoji", "status":
			text, _ := n.Attrs["text"].(string)
			if text == "" {
				text, _ = n.Attrs["shortName"].(string)
			}
			b.WriteString(text)
		case "inlineCard", "blockCard":
			u, _ := n.Attrs["url"].(string)
			b.WriteString(u)
		default:
			b.WriteString(adfInline(n.Content))
		}
	}
	return b.String()
}

type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []adfNode      `json:"marks"`
	Content []adfNode      `json:"content"`
}
