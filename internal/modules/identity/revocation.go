package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"rental-management/internal/platform/apperr"
)

// Revocations remembers in Redis which access tokens and sessions were closed before their natural expiry
// (logout, role switch, password change, stolen refresh token). A key only has to live as long as the access
// tokens it covers, so Redis forgets them by itself.
//
// Every method fails closed: when Redis cannot answer, the request gets a 503 and nothing is let through.
type Revocations struct {
	client *redis.Client
	prefix string
}

// NewRevocations returns the store; prefix is the Redis key prefix of the application.
func NewRevocations(client *redis.Client, prefix string) *Revocations {
	return &Revocations{client: client, prefix: prefix}
}

func (r *Revocations) tokenKey(jti string) string { return r.prefix + "auth:revoked_jti:" + jti }
func (r *Revocations) sessionKey(sid string) string {
	return r.prefix + "auth:revoked_sid:" + sid
}

// minRevocationTTL keeps a key alive long enough to be written at all (Redis refuses a zero expiry).
const minRevocationTTL = time.Second

// RevokeToken blocks one access token until it would have expired anyway.
func (r *Revocations) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	if err := r.client.Set(ctx, r.tokenKey(jti), "1", max(ttl, minRevocationTTL)).Err(); err != nil {
		return apperr.Busy(fmt.Errorf("revoke token: %w", err))
	}
	return nil
}

// RevokeSessions blocks every access token of the sessions for ttl (the life of an access token).
func (r *Revocations) RevokeSessions(ctx context.Context, sids []string, ttl time.Duration) error {
	if len(sids) == 0 {
		return nil
	}
	pipe := r.client.Pipeline()
	for _, sid := range sids {
		pipe.Set(ctx, r.sessionKey(sid), "1", max(ttl, minRevocationTTL))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return apperr.Busy(fmt.Errorf("revoke sessions: %w", err))
	}
	return nil
}

// IsRevoked reports whether the token or its session was revoked.
func (r *Revocations) IsRevoked(ctx context.Context, jti, sid string) (bool, error) {
	values, err := r.client.MGet(ctx, r.tokenKey(jti), r.sessionKey(sid)).Result()
	if err != nil {
		return false, apperr.Busy(fmt.Errorf("check revocation: %w", err))
	}
	for _, v := range values {
		if v != nil {
			return true, nil
		}
	}
	return false, nil
}
