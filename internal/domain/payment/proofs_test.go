package payment

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProofKeysArePrivateAndValid(t *testing.T) {
	key := NewProofKey(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), uuid.MustParse("0192d3f0-0000-7000-8000-00000000abcd"), "webp")
	if !strings.HasPrefix(key, ProofKeyPrefix+"2026/11/") || !strings.HasSuffix(key, ".webp") {
		t.Errorf("key: %s", key)
	}
	if strings.ContainsAny(key, "-ABCDEF") {
		t.Errorf("a key is lowercase hex without dashes: %s", key)
	}
	if got := ThumbKey(key); !strings.HasSuffix(got, "_thumb.webp") || strings.TrimSuffix(got, "_thumb.webp") != strings.TrimSuffix(key, ".webp") {
		t.Errorf("thumb key: %s", got)
	}
	if got := LegacyThumbKey(key); !strings.HasSuffix(got, "_thumb.jpg") || strings.TrimSuffix(got, "_thumb.jpg") != strings.TrimSuffix(key, ".webp") {
		t.Errorf("legacy thumb key: %s", got)
	}
}

func TestParseProofFile(t *testing.T) {
	for name, want := range map[string]struct {
		index int
		thumb bool
		ok    bool
	}{
		"0.webp": {0, false, true}, "12_thumb.webp": {12, true, true}, "2.jpg": {}, "x.webp": {}, "1_thumb.png": {},
		"../1.webp": {}, "1.webp/": {}, "1234.webp": {}, "": {}, "-1.webp": {},
	} {
		i, th, ok := ParseProofFile(name)
		if i != want.index || th != want.thumb || ok != want.ok {
			t.Errorf("ParseProofFile(%q) = %d %v %v", name, i, th, ok)
		}
	}
	if ProofFileName(3, false) != "3.webp" || ProofFileName(3, true) != "3_thumb.webp" {
		t.Error("ProofFileName")
	}
}
