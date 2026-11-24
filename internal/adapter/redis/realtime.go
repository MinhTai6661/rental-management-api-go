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
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"rental-management/internal/adapter/realtime"
	"rental-management/internal/domain/shared"
)

// Broadcaster carries realtime events between API instances through one Pub/Sub channel: every instance listens, and
// each one hands the events to the streams it holds. Pub/Sub keeps nothing, which is the intent: an event nobody is
// connected for is simply lost, and the web reloads its inbox when a stream opens.
type Broadcaster struct {
	client  *redis.Client
	channel string
	log     *slog.Logger
}

// NewBroadcaster returns the broadcaster; prefix is the Redis key prefix of the application.
func NewBroadcaster(client *redis.Client, prefix string, log *slog.Logger) *Broadcaster {
	return &Broadcaster{client: client, channel: prefix + "rt:events", log: log}
}

// Publish sends the event to every instance.
func (b *Broadcaster) Publish(ctx context.Context, env realtime.Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode realtime event: %w", err)
	}
	if err := b.client.Publish(ctx, b.channel, payload).Err(); err != nil {
		return fmt.Errorf("publish realtime event: %w: %w", shared.ErrUnavailable, err)
	}
	return nil
}

// Listen hands every event published on the channel to deliver until ctx ends; go-redis reconnects by itself. A
// payload that is not valid JSON is logged and skipped. Run it in a goroutine; it returns nil when ctx ends.
func (b *Broadcaster) Listen(ctx context.Context, deliver func(realtime.Envelope)) error {
	subscription := b.client.Subscribe(ctx, b.channel)
	defer func() { _ = subscription.Close() }()
	if _, err := subscription.Receive(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("subscribe realtime channel: %w", err)
	}
	messages := subscription.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case message, ok := <-messages:
			if !ok {
				return nil
			}
			var env realtime.Envelope
			if err := json.Unmarshal([]byte(message.Payload), &env); err != nil {
				b.log.Warn("skip a realtime event that is not valid", "error", err)
				continue
			}
			deliver(env)
		}
	}
}

// StreamTickets issues the one-time tickets that open an event stream: a browser EventSource cannot send an
// Authorization header, and the access token must never travel in a URL, so the client trades its token for a ticket
// that works once and for a few seconds.
type StreamTickets struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

// NewStreamTickets returns the store; tickets live for ttl.
func NewStreamTickets(client *redis.Client, prefix string, ttl time.Duration) *StreamTickets {
	return &StreamTickets{client: client, prefix: prefix, ttl: ttl}
}

// TTL is how long a ticket works.
func (t *StreamTickets) TTL() time.Duration { return t.ttl }

type ticketValue struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
}

func (t *StreamTickets) key(token string) string {
	digest := sha256.Sum256([]byte(token))
	return t.prefix + "rt:ticket:" + hex.EncodeToString(digest[:])
}

// Issue makes a ticket for the user acting in role; only the hash of the ticket is stored.
func (t *StreamTickets) Issue(ctx context.Context, userID, sessionID, role string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random ticket: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	value, err := json.Marshal(ticketValue{UserID: userID, SessionID: sessionID, Role: role})
	if err != nil {
		return "", err
	}
	if err := t.client.Set(ctx, t.key(token), value, t.ttl).Err(); err != nil {
		return "", fmt.Errorf("store stream ticket: %w: %w", shared.ErrUnavailable, err)
	}
	return token, nil
}

// Redeem uses the ticket up: the first call returns who it was issued to, any later call (or an expired or unknown
// ticket) is shared.ErrNotFound.
func (t *StreamTickets) Redeem(ctx context.Context, token string) (userID, sessionID, role string, err error) {
	raw, err := t.client.GetDel(ctx, t.key(token)).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", "", shared.ErrNotFound
	}
	if err != nil {
		return "", "", "", fmt.Errorf("redeem stream ticket: %w: %w", shared.ErrUnavailable, err)
	}
	var value ticketValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", "", "", shared.ErrNotFound
	}
	return value.UserID, value.SessionID, value.Role, nil
}
