package admin

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The reasons a rich text document is refused; each is a rule key of the API ("richtext_" + the reason).
const (
	richStructure    = "richtext_structure"
	richNodeType     = "richtext_node_type"
	richMarkType     = "richtext_mark_type"
	richHeadingLevel = "richtext_heading_level"
	richLink         = "richtext_link"
	richTooLarge     = "richtext_too_large"
	richNoHeading    = "richtext_no_heading"
	richEmpty        = "richtext_empty"
)

// Limits of a rich text document.
const (
	maxRichNodes   = 5000
	maxRichDepth   = 6
	maxRichText    = 6000
	maxRichMarks   = 4
	maxRichLinkLen = 2000
)

var linkPattern = regexp.MustCompile(`(?i)^(https?://|mailto:)\S+$`)

// childrenOf lists the node types each node type may contain; text nodes contain nothing.
var childrenOf = map[string][]string{
	"doc":         {"heading", "paragraph", "bulletList", "orderedList"},
	"bulletList":  {"listItem"},
	"orderedList": {"listItem"},
	"listItem":    {"paragraph", "bulletList", "orderedList"},
	"heading":     {"text"},
	"paragraph":   {"text"},
	"text":        nil,
}

var allowedNodeKeys = map[string]bool{"type": true, "content": true, "attrs": true, "text": true, "marks": true}

var allowedMarks = map[string]bool{"bold": true, "italic": true, "underline": true, "link": true}

// CheckRichText validates a TipTap (ProseMirror) document against the white list: headings of level 2 and 3,
// paragraphs, lists, text, and the marks bold, italic, underline and link. It returns the rule key of the first
// problem, or "" when the document is fine. The text must hold at least one section heading (level 2) and one letter.
func CheckRichText(raw json.RawMessage) string {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return richStructure
	}
	root, ok := doc.(map[string]any)
	if !ok || root["type"] != "doc" {
		return richStructure
	}
	if _, ok := root["content"].([]any); !ok {
		return richStructure
	}
	w := &richWalk{}
	if reason := w.node(root, 0); reason != "" {
		return reason
	}
	switch {
	case !w.heading:
		return richNoHeading
	case !w.letter:
		return richEmpty
	}
	return ""
}

type richWalk struct {
	nodes   int
	heading bool
	letter  bool
}

func (w *richWalk) node(n map[string]any, depth int) string {
	if w.nodes++; w.nodes > maxRichNodes || depth > maxRichDepth {
		return richTooLarge
	}
	for k := range n {
		if !allowedNodeKeys[k] {
			return richStructure
		}
	}
	typ, _ := n["type"].(string)
	allowed, known := childrenOf[typ]
	if !known {
		return richNodeType
	}
	if typ == "text" {
		return w.text(n)
	}
	if _, has := n["text"]; has || n["marks"] != nil {
		return richStructure
	}
	if reason := w.attrs(typ, n); reason != "" {
		return reason
	}
	children, present := n["content"]
	if !present {
		return ""
	}
	list, ok := children.([]any)
	if !ok {
		return richStructure
	}
	for _, c := range list {
		child, ok := c.(map[string]any)
		if !ok {
			return richStructure
		}
		ct, _ := child["type"].(string)
		if !contains(allowed, ct) {
			if _, exists := childrenOf[ct]; !exists {
				return richNodeType
			}
			return richStructure
		}
		if reason := w.node(child, depth+1); reason != "" {
			return reason
		}
	}
	return ""
}

func (w *richWalk) attrs(typ string, n map[string]any) string {
	attrs, has := n["attrs"]
	if typ != "heading" {
		if has {
			return richStructure
		}
		return ""
	}
	m, ok := attrs.(map[string]any)
	if !ok {
		return richHeadingLevel
	}
	level, _ := m["level"].(float64)
	if level != 2 && level != 3 {
		return richHeadingLevel
	}
	if level == 2 {
		w.heading = true
	}
	return ""
}

func (w *richWalk) text(n map[string]any) string {
	if _, has := n["content"]; has {
		return richStructure
	}
	if _, has := n["attrs"]; has {
		return richStructure
	}
	text, ok := n["text"].(string)
	if !ok || text == "" || utf8.RuneCountInString(text) > maxRichText {
		return richStructure
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			w.letter = true
			break
		}
	}
	if marks, has := n["marks"]; has {
		list, ok := marks.([]any)
		if !ok || len(list) > maxRichMarks {
			return richStructure
		}
		for _, m := range list {
			if reason := checkMark(m); reason != "" {
				return reason
			}
		}
	}
	return ""
}

func checkMark(m any) string {
	mark, ok := m.(map[string]any)
	if !ok {
		return richStructure
	}
	typ, _ := mark["type"].(string)
	if !allowedMarks[typ] {
		return richMarkType
	}
	if typ != "link" {
		return ""
	}
	attrs, _ := mark["attrs"].(map[string]any)
	href, _ := attrs["href"].(string)
	if href == "" || len(href) > maxRichLinkLen || !linkPattern.MatchString(strings.TrimSpace(href)) {
		return richLink
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
