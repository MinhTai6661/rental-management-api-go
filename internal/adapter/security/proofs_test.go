package security

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newLinks(t *testing.T, master byte) *ProofLinks {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = master
	}
	p, err := NewProofLinks(key, "https://api.example.vn/")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// parse splits a link into the pieces Valid takes.
func parse(t *testing.T, raw string) (file string, expires int64, signature string) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	expires, err = strconv.ParseInt(u.Query().Get("expires"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path[strings.LastIndex(u.Path, "/")+1:], expires, u.Query().Get("signature")
}

func TestProofLinkIsAbsoluteSignedAndExpires(t *testing.T) {
	p := newLinks(t, 1)
	id := uuid.New()
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	link := p.Link(id, "0.webp", now.Add(2*time.Hour))
	if !strings.HasPrefix(link, "https://api.example.vn/v1/payments/"+id.String()+"/proofs/0.webp?") {
		t.Fatalf("link: %s", link)
	}
	file, expires, sig := parse(t, link)
	if !p.Valid(id, file, expires, sig, now) {
		t.Error("a fresh link is valid")
	}
	if !p.Valid(id, file, expires, sig, now.Add(time.Hour+59*time.Minute)) {
		t.Error("valid until it expires")
	}
	if p.Valid(id, file, expires, sig, now.Add(2*time.Hour)) {
		t.Error("an expired link is refused")
	}
}

func TestProofLinkCannotBeTamperedWith(t *testing.T) {
	p := newLinks(t, 1)
	id := uuid.New()
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	_, expires, sig := parse(t, p.Link(id, "0.webp", now.Add(time.Hour)))

	// Change the first digit to a different one (writing a fixed "0" would leave 1 in 16 signatures untouched).
	flipped := "0" + sig[1:]
	if sig[0] == '0' {
		flipped = "1" + sig[1:]
	}

	for name, ok := range map[string]bool{
		"another file":      p.Valid(id, "1.webp", expires, sig, now),
		"the small copy":    p.Valid(id, "0_thumb.webp", expires, sig, now),
		"another payment":   p.Valid(uuid.New(), "0.webp", expires, sig, now),
		"a later expiry":    p.Valid(id, "0.webp", expires+3600, sig, now),
		"another key":       newLinks(t, 2).Valid(id, "0.webp", expires, sig, now),
		"no signature":      p.Valid(id, "0.webp", expires, "", now),
		"not hex":           p.Valid(id, "0.webp", expires, strings.Repeat("zz", 32), now),
		"a short signature": p.Valid(id, "0.webp", expires, sig[:32], now),
		"a flipped bit":     p.Valid(id, "0.webp", expires, flipped, now),
	} {
		if ok {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestProofLinksNeedAFullKey(t *testing.T) {
	if _, err := NewProofLinks([]byte("short"), "https://x"); err == nil {
		t.Error("a short key is refused")
	}
}

func TestProofKeyIsNotTheOptOutKey(t *testing.T) {
	master := make([]byte, 32)
	links, _ := NewProofLinks(master, "https://x")
	opt, _ := NewOptOutSigner(master)
	if string(links.key) == string(opt.key) {
		t.Error("each use of the master key has its own derived key")
	}
}
