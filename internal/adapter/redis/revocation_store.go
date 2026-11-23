// Package redisstore keeps the revoked tokens and sessions in Redis; it is the only code that imports go-redis.
package redisstore

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"rental-management/internal/domain/shared"
)

// RevocationStore remembers in Redis which access tokens and sessions were closed before their natural expiry
// (logout, role switch, password change, stolen refresh token). A key only has to live as long as the access
// tokens it covers, so Redis forgets them by itself.
//
// Every method fails closed: when Redis cannot answer, the error wraps shared.ErrUnavailable (the HTTP adapter
// answers 503) and nothing is let through.
type RevocationStore struct {
	client *redis.Client
	prefix string
}

// NewRevocationStore returns the store; prefix is the Redis key prefix of the application.
func NewRevocationStore(client *redis.Client, prefix string) *RevocationStore {
	return &RevocationStore{client: client, prefix: prefix}
}

func (r *RevocationStore) tokenKey(jti string) string {
	return r.prefix + "auth:revoked_jti:" + jti
}
func (r *RevocationStore) sessionKey(sid string) string {
	return r.prefix + "auth:revoked_sid:" + sid
}

// minRevocationTTL keeps a key alive long enough to be written at all (Redis refuses a zero expiry).
const minRevocationTTL = time.Second

// RevokeToken blocks one access token until it would have expired anyway.
func (r *RevocationStore) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	if err := r.client.Set(ctx, r.tokenKey(jti), "1", max(ttl, minRevocationTTL)).Err(); err != nil {
		return fmt.Errorf("revoke token: %w: %w", shared.ErrUnavailable, err)
	}
	return nil
}

// RevokeSessions blocks every access token of the sessions for ttl (the life of an access token).
func (r *RevocationStore) RevokeSessions(ctx context.Context, sids []string, ttl time.Duration) error {
	if len(sids) == 0 {
		return nil
	}
	pipe := r.client.Pipeline()
	for _, sid := range sids {
		pipe.Set(ctx, r.sessionKey(sid), "1", max(ttl, minRevocationTTL))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("revoke sessions: %w: %w", shared.ErrUnavailable, err)
	}
	return nil
}

// IsRevoked reports whether the token or its session was revoked.
func (r *RevocationStore) IsRevoked(ctx context.Context, jti, sid string) (bool, error) {
	values, err := r.client.MGet(ctx, r.tokenKey(jti), r.sessionKey(sid)).Result()
	if err != nil {
		return false, fmt.Errorf("check revocation: %w: %w", shared.ErrUnavailable, err)
	}
	for _, v := range values {
		if v != nil {
			return true, nil
		}
	}
	return false, nil
}
