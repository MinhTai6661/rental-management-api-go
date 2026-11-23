package property

import (
	"testing"
	"time"
)

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

func TestMediaKeys(t *testing.T) {
	var random [16]byte
	for i := range random {
		random[i] = byte(i)
	}
	file, thumb := mediaKeys(time.Date(2026, time.March, 5, 10, 0, 0, 0, time.UTC), random, "webp")
	if file != "property/2026/03/000102030405060708090a0b0c0d0e0f.webp" || thumb != "property/2026/03/000102030405060708090a0b0c0d0e0f_thumb.jpg" {
		t.Fatalf("got %s %s", file, thumb)
	}
}
