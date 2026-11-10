package service

import (
	"fmt"
	"regexp"
	"strings"
)

var notebookMermaidBlock = regexp.MustCompile("(?m)^```mermaid[ \\t]*\\n([\\s\\S]*?)^```[ \\t]*$")
var notebookMermaidDirection = regexp.MustCompile(`^(flowchart|graph)\s+(TB|TD|BT|RL|LR)$`)
var notebookAnonymousEdge = regexp.MustCompile(`^\s*"([^"\r\n]*)"\s*-->\s*"([^"\r\n]*)"\s*;?\s*$`)

// Repair only the observed all-anonymous flowchart dialect. Every edge must
// match, so valid Mermaid and other syntaxes are left byte-for-byte unchanged.
// Assigning stable IDs preserves identical labels, edge order and direction.
func repairNotebookMermaid(content string) string {
	return notebookMermaidBlock.ReplaceAllStringFunc(content, func(block string) string {
		match := notebookMermaidBlock.FindStringSubmatch(block)
		lines := strings.Split(strings.TrimSpace(match[1]), "\n")
		if len(lines) < 2 || !notebookMermaidDirection.MatchString(strings.TrimSpace(lines[0])) {
			return block
		}
		ids := map[string]string{}
		nodes, edges := []string{}, []string{}
		node := func(label string) string {
			if id, ok := ids[label]; ok {
				return id
			}
			id := fmt.Sprintf("DRN%d", len(ids)+1)
			ids[label] = id
			// Treat model labels as text. Encode markup instead of allowing HTML
			// or Mermaid configuration to enter through a repaired node label.
			label = strings.NewReplacer("&", "#38;", "<", "#60;", ">", "#62;", "\\n", "<br/>").Replace(label)
			nodes = append(nodes, id+`["`+label+`"]`)
			return id
		}
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			edge := notebookAnonymousEdge.FindStringSubmatch(line)
			if edge == nil {
				return block
			}
			from, to := node(edge[1]), node(edge[2])
			edges = append(edges, from+" --> "+to)
		}
		if len(edges) == 0 {
			return block
		}
		return "```mermaid\n" + strings.TrimSpace(lines[0]) + "\n    " + strings.Join(append(nodes, edges...), "\n    ") + "\n```"
	})
}
