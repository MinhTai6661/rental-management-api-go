package security

import (
	"bytes"
	"strings"
	"testing"
)

func master(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newSigner(t *testing.T, b byte) *OptOutSigner {
	t.Helper()
	s, err := NewOptOutSigner(master(b))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOptOutSignerIsDeterministicAndVerifies(t *testing.T) {
	s := newSigner(t, 1)
	token := s.Sign("a@example.vn")
	if len(token) != 64 || token != s.Sign("a@example.vn") {
		t.Fatalf("token %q must be 64 hex characters and stable", token)
	}
	if !s.Verify("a@example.vn", token) {
		t.Error("its own token verifies")
	}
	if s.Sign("b@example.vn") == token || s.Verify("b@example.vn", token) {
		t.Error("another address must not share a token")
	}
}

func TestOptOutSignerRefusesTamperedTokens(t *testing.T) {
	s := newSigner(t, 1)
	token := s.Sign("a@example.vn")
	flipped := "0" + token[1:]
	if token[0] == '0' {
		flipped = "1" + token[1:]
	}
	for name, bad := range map[string]string{
		"empty": "", "short": token[:63], "long": token + "0", "not hex": strings.Repeat("z", 64), "flipped": flipped,
		"upper case differs only in text": strings.ToUpper(token) + "x",
	} {
		if s.Verify("a@example.vn", bad) {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOptOutSignerKeyDependsOnTheMasterKeyAndItsLength(t *testing.T) {
	if newSigner(t, 1).Sign("a@example.vn") == newSigner(t, 2).Sign("a@example.vn") {
		t.Error("a different master key gives a different token")
	}
	if _, err := NewOptOutSigner(make([]byte, 31)); err == nil {
		t.Error("a 31-byte master key must be refused")
	}
	s := newSigner(t, 1)
	if s.Verify("a@example.vn", newSigner(t, 2).Sign("a@example.vn")) {
		t.Error("a token of another key must not verify")
	}
}
