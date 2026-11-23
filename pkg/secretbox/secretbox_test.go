package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func box(t *testing.T, fill byte) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealThenOpenGivesTheTextBack(t *testing.T) {
	b := box(t, 1)
	for _, plain := range []string{"", "a", strings.Repeat("x", 64), "tiếng Việt ✓"} {
		sealed, err := b.Seal(plain)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := b.Open(sealed); err != nil || got != plain {
			t.Errorf("Open(Seal(%q)) = %q, %v", plain, got, err)
		}
	}
}

func TestTheSameTextSealsToDifferentValuesAndHidesTheText(t *testing.T) {
	b := box(t, 1)
	one, _ := b.Seal("secret-token")
	two, _ := b.Seal("secret-token")
	if one == two {
		t.Error("a fresh nonce makes every sealed value different")
	}
	if strings.Contains(one, "secret-token") {
		t.Error("the sealed value must not hold the text")
	}
}

func TestAWrongKeyOrAChangedValueDoesNotOpen(t *testing.T) {
	sealed, _ := box(t, 1).Seal("secret-token")
	if _, err := box(t, 2).Open(sealed); !errors.Is(err, ErrOpen) {
		t.Errorf("another key: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 0xff
	for name, bad := range map[string]string{
		"a flipped bit": base64.StdEncoding.EncodeToString(raw),
		"truncated":     sealed[:10],
		"empty":         "",
		"not base64":    "!!!",
		"plain text":    "secret-token",
	} {
		if _, err := box(t, 1).Open(bad); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNewWantsExactlyAKeyOfTheRightSize(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := New(make([]byte, n)); err == nil {
			t.Errorf("a key of %d bytes must be refused", n)
		}
	}
}

func TestParseKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, KeySize))
	if key, err := ParseKey(good); err != nil || len(key) != KeySize {
		t.Errorf("good key: %v", err)
	}
	for name, bad := range map[string]string{
		"not base64": "***", "too short": base64.StdEncoding.EncodeToString([]byte("short")), "empty": "",
	} {
		_, err := ParseKey(bad)
		if err == nil {
			t.Errorf("%s must be refused", name)
		} else if strings.Contains(err.Error(), bad) && bad != "" {
			t.Errorf("%s: the error must not repeat the value: %v", name, err)
		}
	}
}
