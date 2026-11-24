package admin

import (
	"strings"
	"testing"
)

func TestCheckRichText(t *testing.T) {
	h2 := `{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"1. Scope"}]}`
	doc := func(nodes ...string) string { return `{"type":"doc","content":[` + strings.Join(nodes, ",") + `]}` }
	para := func(inner string) string { return `{"type":"paragraph","content":[` + inner + `]}` }
	text := func(s string, marks string) string {
		if marks != "" {
			return `{"type":"text","text":"` + s + `","marks":[` + marks + `]}`
		}
		return `{"type":"text","text":"` + s + `"}`
	}
	deep := h2
	list := `{"type":"paragraph","content":[` + text("x", "") + `]}`
	for i := 0; i < 4; i++ { // list > item > list > item ... deeper than 6 levels
		list = `{"type":"bulletList","content":[{"type":"listItem","content":[` + list + `]}]}`
	}
	tests := []struct {
		name, json, want string
	}{
		{"valid", doc(h2, para(text("hello", `{"type":"bold"}`))), ""},
		{"valid link", doc(h2, para(text("a", `{"type":"link","attrs":{"href":"HTTPS://a.vn/x"}}`))), ""},
		{"not json", `nope`, richStructure},
		{"not a doc", `{"type":"paragraph"}`, richStructure},
		{"no content", `{"type":"doc"}`, richStructure},
		{"script node", doc(h2, `{"type":"script"}`), richNodeType},
		{"extra key", doc(h2, `{"type":"paragraph","onclick":"x"}`), richStructure},
		{"heading level 1", doc(`{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"x"}]}`), richHeadingLevel},
		{"heading without attrs", doc(`{"type":"heading","content":[{"type":"text","text":"x"}]}`), richHeadingLevel},
		{"bad mark", doc(h2, para(text("a", `{"type":"code"}`))), richMarkType},
		{"javascript link", doc(h2, para(text("a", `{"type":"link","attrs":{"href":"javascript:alert(1)"}}`))), richLink},
		{"too many marks", doc(h2, para(text("a", strings.Repeat(`{"type":"bold"},`, 4)+`{"type":"italic"}`))), richStructure},
		{"list in a paragraph", doc(h2, para(list)), richStructure},
		{"empty text node", doc(h2, para(`{"type":"text","text":""}`)), richStructure},
		{"no section heading", doc(`{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"x"}]}`), richNoHeading},
		{"no letters", doc(`{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"-- "}]}`), richEmpty},
		{"too deep", doc(h2, deep, list), richTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CheckRichText([]byte(tc.json)); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}
