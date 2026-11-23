package property

import "testing"

func TestMediaETag(t *testing.T) {
	if got := mediaETag("property/2026/11/abc123_thumb.jpg"); got != `"abc123_thumb.jpg"` {
		t.Fatalf("got %s", got)
	}
}

func TestETagMatches(t *testing.T) {
	const tag = `"abc.webp"`
	cases := []struct {
		header string
		want   bool
	}{
		{`"abc.webp"`, true},
		{`W/"abc.webp"`, true},
		{`"x.webp", "abc.webp"`, true},
		{`"x.webp" , W/"abc.webp"`, true},
		{`*`, true},
		{`"abc.jpg"`, false},
		{`abc.webp`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := etagMatches(c.header, tag); got != c.want {
			t.Errorf("etagMatches(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}
