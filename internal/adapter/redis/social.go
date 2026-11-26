package redisstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
)

// socialKeyBytes makes a key of 43 characters (32 random bytes in unpadded base64url).
const socialKeyBytes = 32

var socialKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// oneUseStore keeps plain JSON under a random key that works once: the key is handed to the person, only its hash is
// used as the Redis key, and GETDEL reads and deletes in one step so two callbacks cannot both use it.
type oneUseStore struct {
	client *redis.Client
	prefix string
	kind   string
}

func (s oneUseStore) redisKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return s.prefix + s.kind + ":" + hex.EncodeToString(digest[:])
}

func (s oneUseStore) put(ctx context.Context, value any, ttl time.Duration) (string, error) {
	raw := make([]byte, socialKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random %s key: %w", s.kind, err)
	}
	key := base64.RawURLEncoding.EncodeToString(raw)
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, s.redisKey(key), body, ttl).Err(); err != nil {
		return "", fmt.Errorf("store %s: %w: %w", s.kind, shared.ErrUnavailable, err)
	}
	return key, nil
}

// take returns the stored JSON and deletes it; shared.ErrNotFound for a key that is malformed, unknown, used or expired.
func (s oneUseStore) take(ctx context.Context, key string) ([]byte, error) {
	if !socialKeyPattern.MatchString(key) {
		return nil, shared.ErrNotFound
	}
	body, err := s.client.GetDel(ctx, s.redisKey(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("take %s: %w: %w", s.kind, shared.ErrUnavailable, err)
	}
	return body, nil
}

// SocialStateStore keeps the state of a round trip to a provider (who is signing in or linking, where to return, the
// PKCE verifier) while the person is at the provider. Plain data, one use, short life: a forged or replayed callback
// finds nothing, and the verifier never goes through the browser.
type SocialStateStore struct{ store oneUseStore }

// NewSocialStateStore returns the store; prefix is the Redis key prefix of the application.
func NewSocialStateStore(client *redis.Client, prefix string) *SocialStateStore {
	return &SocialStateStore{store: oneUseStore{client: client, prefix: prefix, kind: "social:state"}}
}

type socialStateValue struct {
	Provider     string `json:"provider"`
	Intent       string `json:"intent"`
	UserID       string `json:"user_id,omitempty"`
	ActorRole    string `json:"actor_role,omitempty"`
	ReturnPath   string `json:"return_path"`
	CodeVerifier string `json:"code_verifier"`
}

// Put stores the state for ttl and returns the key to send as the OAuth `state`.
func (s *SocialStateStore) Put(ctx context.Context, state domain.SocialState, ttl time.Duration) (string, error) {
	value := socialStateValue{
		Provider: state.Provider, Intent: string(state.Intent), ActorRole: state.ActorRole, ReturnPath: state.ReturnPath,
		CodeVerifier: state.CodeVerifier,
	}
	if state.UserID != nil {
		value.UserID = state.UserID.String()
	}
	return s.store.put(ctx, value, ttl)
}

// Take returns the state and deletes it; shared.ErrNotFound when there is none (or it is incomplete).
func (s *SocialStateStore) Take(ctx context.Context, key string) (domain.SocialState, error) {
	body, err := s.store.take(ctx, key)
	if err != nil {
		return domain.SocialState{}, err
	}
	var value socialStateValue
	if err := json.Unmarshal(body, &value); err != nil || value.Provider == "" || value.CodeVerifier == "" {
		return domain.SocialState{}, shared.ErrNotFound
	}
	state := domain.SocialState{
		Provider: value.Provider, Intent: domain.SocialIntent(value.Intent), ActorRole: value.ActorRole,
		ReturnPath: value.ReturnPath, CodeVerifier: value.CodeVerifier,
	}
	if value.UserID != "" {
		id, err := uuid.Parse(value.UserID)
		if err != nil {
			return domain.SocialState{}, shared.ErrNotFound
		}
		state.UserID = &id
	}
	return state, nil
}

// LoginCodeStore keeps the one-time codes that stand for a completed social sign-in: the redirect carries the code,
// never tokens, and the web app trades it at POST /auth/social/exchange.
type LoginCodeStore struct{ store oneUseStore }

// NewLoginCodeStore returns the store; prefix is the Redis key prefix of the application.
func NewLoginCodeStore(client *redis.Client, prefix string) *LoginCodeStore {
	return &LoginCodeStore{store: oneUseStore{client: client, prefix: prefix, kind: "social:code"}}
}

type loginGrantValue struct {
	UserID string `json:"user_id"`
	Via    string `json:"via"`
}

// Put stores the grant for ttl and returns the code.
func (s *LoginCodeStore) Put(ctx context.Context, grant domain.SocialLoginGrant, ttl time.Duration) (string, error) {
	return s.store.put(ctx, loginGrantValue{UserID: grant.UserID.String(), Via: grant.Via}, ttl)
}

// Take returns the grant and deletes it; shared.ErrNotFound when the code is unknown, used or expired.
func (s *LoginCodeStore) Take(ctx context.Context, code string) (domain.SocialLoginGrant, error) {
	body, err := s.store.take(ctx, code)
	if err != nil {
		return domain.SocialLoginGrant{}, err
	}
	var value loginGrantValue
	if err := json.Unmarshal(body, &value); err != nil {
		return domain.SocialLoginGrant{}, shared.ErrNotFound
	}
	id, err := uuid.Parse(value.UserID)
	if err != nil {
		return domain.SocialLoginGrant{}, shared.ErrNotFound
	}
	return domain.SocialLoginGrant{UserID: id, Via: value.Via}, nil
}
