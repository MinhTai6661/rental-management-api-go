package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// accessAlgorithm is the only signing algorithm accepted for access tokens.
const accessAlgorithm = "HS256"

// refreshTokenBytes is the size of the random part of refresh and verification tokens (256 bits).
const refreshTokenBytes = 32

// Claims is what an access token says about its holder.
type Claims struct {
	UserID    string    // sub
	SessionID string    // sid: the login session; revoking it kills every token of the session
	Role      string    // the role the token acts in
	TokenID   string    // jti: unique per token, used to revoke one token
	ExpiresAt time.Time // exp
}

// accessClaims are the claims as they travel in the JWT.
type accessClaims struct {
	SessionID string `json:"sid"`
	Role      string `json:"role"`
	jwt.RegisteredClaims
}

// TokenIssuer signs and verifies access tokens (JWT, HS256).
type TokenIssuer struct {
	secret []byte
	issuer string
	ttl    time.Duration
	parser *jwt.Parser
	now    func() time.Time
}

// NewTokenIssuer returns an issuer. ttl is the life of an access token, leeway the clock skew tolerated when
// verifying, now the clock (time.Now in production).
func NewTokenIssuer(secret, issuer string, ttl, leeway time.Duration, now func() time.Time) *TokenIssuer {
	return &TokenIssuer{
		secret: []byte(secret),
		issuer: issuer,
		ttl:    ttl,
		now:    now,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{accessAlgorithm}), // also rules out alg "none"
			jwt.WithIssuer(issuer),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithLeeway(leeway),
			jwt.WithTimeFunc(now),
		),
	}
}

// TTL is the life of an access token.
func (t *TokenIssuer) TTL() time.Duration { return t.ttl }

// Sign returns a new access token for the user's session acting in role.
func (t *TokenIssuer) Sign(userID, sessionID uuid.UUID, role string) (string, Claims, error) {
	now := t.now()
	jti, err := uuid.NewV7()
	if err != nil {
		return "", Claims{}, fmt.Errorf("make token id: %w", err)
	}
	expires := now.Add(t.ttl)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims{
		SessionID: sessionID.String(),
		Role:      role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   userID.String(),
			ID:        jti.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	})
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", Claims{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, Claims{
		UserID: userID.String(), SessionID: sessionID.String(), Role: role, TokenID: jti.String(), ExpiresAt: expires,
	}, nil
}

// errInvalidToken is returned for every token that cannot be trusted; the reason is not told to the client.
var errInvalidToken = errors.New("invalid access token")

// Verify checks the signature, algorithm, issuer and expiry, and that the claims the API relies on are present.
func (t *TokenIssuer) Verify(raw string) (Claims, error) {
	var c accessClaims
	token, err := t.parser.ParseWithClaims(raw, &c, func(*jwt.Token) (any, error) { return t.secret, nil })
	if err != nil || !token.Valid {
		return Claims{}, fmt.Errorf("%w: %w", errInvalidToken, err)
	}
	if c.Subject == "" || c.ID == "" || c.SessionID == "" || c.Role == "" || c.ExpiresAt == nil {
		return Claims{}, fmt.Errorf("%w: a required claim is missing", errInvalidToken)
	}
	return Claims{
		UserID: c.Subject, SessionID: c.SessionID, Role: c.Role, TokenID: c.ID, ExpiresAt: c.ExpiresAt.Time,
	}, nil
}

// NewOpaqueToken returns a random token to hand to a client (a refresh token, an email verification token) and the
// SHA-256 hash to store. Only the hash is ever kept: whoever reads the database cannot use the tokens in it.
func NewOpaqueToken() (raw string, hash []byte, err error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("make token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

// HashToken is the SHA-256 of a token as sent by a client; it is how tokens are looked up.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
