package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "0123456789abcdef0123456789abcdef"

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func newTestIssuer(clock *testClock) *TokenIssuer {
	return NewTokenIssuer(testSecret, "rental-management", 15*time.Minute, 5*time.Second, clock.now)
}

func TestSignAndVerify(t *testing.T) {
	clock := &testClock{time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	issuer := newTestIssuer(clock)
	user, session := uuid.New(), uuid.New()

	raw, signed, err := issuer.Sign(user, session, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	got, err := issuer.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != signed.TokenID || !got.ExpiresAt.Equal(signed.ExpiresAt) || got.UserID != user.String() || got.SessionID != session.String() || got.Role != "tenant" ||
		got.TokenID == "" || !got.ExpiresAt.Equal(clock.t.Add(15*time.Minute)) {
		t.Fatalf("claims differ: signed %+v, verified %+v", signed, got)
	}
	if issuer.TTL() != 15*time.Minute {
		t.Errorf("TTL = %s", issuer.TTL())
	}

	// Every token has its own id.
	other, again, _ := issuer.Sign(user, session, "tenant")
	if other == raw || again.TokenID == got.TokenID {
		t.Error("two tokens must not share a jti")
	}
}

func TestVerifyChecksExpiryWithLeeway(t *testing.T) {
	clock := &testClock{time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	issuer := newTestIssuer(clock)
	raw, _, _ := issuer.Sign(uuid.New(), uuid.New(), "tenant")

	clock.t = clock.t.Add(15*time.Minute + 4*time.Second) // inside the 5 s leeway
	if _, err := issuer.Verify(raw); err != nil {
		t.Errorf("a token just past its expiry is within the leeway: %v", err)
	}
	clock.t = clock.t.Add(2 * time.Second) // 6 s past exp
	if _, err := issuer.Verify(raw); err == nil {
		t.Error("an expired token must be refused")
	}
}

func TestVerifyRefusesUntrustedTokens(t *testing.T) {
	clock := &testClock{time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	issuer := newTestIssuer(clock)
	good, _, _ := issuer.Sign(uuid.New(), uuid.New(), "tenant")

	claims := func(m jwt.MapClaims) jwt.MapClaims {
		base := jwt.MapClaims{
			"iss": "rental-management", "sub": uuid.NewString(), "sid": uuid.NewString(), "role": "tenant",
			"jti": uuid.NewString(), "iat": clock.t.Unix(), "exp": clock.t.Add(time.Minute).Unix(),
		}
		for k, v := range m {
			if v == nil {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return base
	}
	sign := func(method jwt.SigningMethod, key any, c jwt.MapClaims) string {
		s, err := jwt.NewWithClaims(method, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	secret := []byte(testSecret)

	// Flip one character of the signature.
	i := strings.LastIndex(good, ".") + 3
	flipped := good[:i] + string(map[bool]byte{true: 'B', false: 'A'}[good[i] == 'A']) + good[i+1:]

	cases := map[string]string{
		"empty":                "",
		"garbage":              "not.a.token",
		"tampered signature":   flipped,
		"other secret":         sign(jwt.SigningMethodHS256, []byte("another-secret-another-secret-0123"), claims(nil)),
		"other algorithm":      sign(jwt.SigningMethodHS512, secret, claims(nil)),
		"alg none":             sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claims(nil)),
		"wrong issuer":         sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"iss": "someone-else"})),
		"no issuer":            sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"iss": nil})),
		"no expiry":            sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"exp": nil})),
		"expired":              sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"exp": clock.t.Add(-time.Hour).Unix()})),
		"issued in the future": sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"iat": clock.t.Add(time.Hour).Unix()})),
		"no subject":           sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"sub": nil})),
		"no token id":          sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"jti": nil})),
		"no session":           sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"sid": nil})),
		"no role":              sign(jwt.SigningMethodHS256, secret, claims(jwt.MapClaims{"role": nil})),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := issuer.Verify(raw); err == nil {
				t.Errorf("%s must be refused", name)
			}
		})
	}

	// The control: the same helper builds a token the issuer accepts.
	if _, err := issuer.Verify(sign(jwt.SigningMethodHS256, secret, claims(nil))); err != nil {
		t.Fatalf("the control token must be accepted: %v", err)
	}
}

func TestOpaqueTokens(t *testing.T) {
	raw, hash, err := NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 || len(hash) != 32 { // 32 bytes in unpadded base64url, and a SHA-256
		t.Fatalf("raw %d chars, hash %d bytes", len(raw), len(hash))
	}
	if string(HashToken(raw)) != string(hash) {
		t.Error("the stored hash must be the hash of the raw token")
	}
	if strings.Contains(string(hash), raw) {
		t.Error("the hash must not contain the token")
	}
	other, otherHash, _ := NewOpaqueToken()
	if other == raw || string(otherHash) == string(hash) {
		t.Error("tokens must be random")
	}
	if string(HashToken("a")) == string(HashToken("b")) {
		t.Error("different tokens must hash differently")
	}
}
