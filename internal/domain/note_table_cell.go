package domain

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	htmlparser "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Decode both API input and stored documents through the same formatting
// allowlist. Entity-escaped text stays text; it is never decoded into markup.
func (c *TableCell) UnmarshalJSON(data []byte) error {
	type plain TableCell
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = TableCell(value)
	return c.normalize()
}

func (c *TableCell) normalize() error {
	if len(c.Text) > 1024*1024 || len(c.HTML) > 1024*1024 {
		return fmt.Errorf("table cell exceeds 1 MiB")
	}
	if c.HTML == "" {
		return nil // Older text-only documents keep their existing representation.
	}
	nodes, err := htmlparser.ParseFragment(strings.NewReader(c.HTML), &htmlparser.Node{Type: htmlparser.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return fmt.Errorf("invalid table cell HTML")
	}
	var formatted, text strings.Builder
	lineBreak := func() {
		if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
			text.WriteByte('\n')
		}
	}
	var visit func(*htmlparser.Node, int)
	visit = func(n *htmlparser.Node, depth int) {
		if depth > 100 {
			return
		}
		if n.Type == htmlparser.TextNode {
			formatted.WriteString(html.EscapeString(n.Data))
			text.WriteString(n.Data)
			return
		}
		tag := ""
		if n.Type == htmlparser.ElementNode {
			switch n.Data {
			case "script", "style", "iframe", "object", "embed", "svg", "math", "template", "noscript", "textarea", "xmp", "plaintext", "title":
				return
			case "b", "strong", "i", "em", "s", "strike", "del", "u", "span", "font", "br", "div", "p", "ul", "ol", "li", "sub", "sup", "code":
				tag = n.Data
			}
		}
		block := tag == "div" || tag == "p" || tag == "li"
		if block {
			lineBreak()
		}
		if tag != "" {
			formatted.WriteString("<" + tag)
			for _, a := range n.Attr {
				if a.Namespace != "" {
					continue
				}
				if a.Key == "style" {
					if style := safeNoteCellStyle(a.Val); style != "" {
						formatted.WriteString(` style="` + html.EscapeString(style) + `"`)
					}
				} else if tag == "font" && a.Key == "color" && validNoteCellColor(a.Val) {
					formatted.WriteString(` color="` + html.EscapeString(a.Val) + `"`)
				}
			}
			formatted.WriteByte('>')
		}
		if tag == "br" {
			text.WriteByte('\n')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child, depth+1)
		}
		if tag != "" && tag != "br" {
			formatted.WriteString("</" + tag + ">")
		}
		if block {
			lineBreak()
		}
	}
	for _, node := range nodes {
		visit(node, 0)
	}
	c.HTML = formatted.String()
	c.Text = strings.TrimRight(text.String(), "\n")
	return nil
}

func validNoteCellColor(value string) bool {
	return value == "transparent" || validNoteMark("color:"+value)
}

func safeNoteCellStyle(style string) string {
	var kept []string
	for _, declaration := range strings.Split(style, ";") {
		property, value, ok := strings.Cut(declaration, ":")
		if !ok {
			continue
		}
		property, value = strings.ToLower(strings.TrimSpace(property)), strings.ToLower(strings.TrimSpace(value))
		valid := false
		switch property {
		case "color", "background-color":
			valid = validNoteCellColor(value)
		case "font-weight":
			valid = value == "bold" || value == "normal" || len(value) == 3 && value[0] >= '1' && value[0] <= '9' && value[1:] == "00"
		case "font-style":
			valid = value == "italic" || value == "normal" || value == "oblique"
		case "text-decoration", "text-decoration-line":
			valid = value == "none" || value == "underline" || value == "line-through" || value == "underline line-through" || value == "line-through underline"
		case "text-align":
			valid = value == "left" || value == "right" || value == "center" || value == "justify"
		}
		if valid {
			kept = append(kept, property+": "+value)
		}
	}
	return strings.Join(kept, "; ")
}
