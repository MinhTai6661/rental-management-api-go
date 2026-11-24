package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"rental-management/config"
	"rental-management/internal/adapter/realtime"
	redisstore "rental-management/internal/adapter/redis"
	domain "rental-management/internal/domain/notification"
	notificationuc "rental-management/internal/usecase/notification"
)

// pushChunkUsers is how many people one realtime envelope names, so a message to everybody stays a handful of small
// Redis messages.
const pushChunkUsers = 100

// publisher is what the pusher needs of Redis; *redisstore.Broadcaster is one.
type publisher interface {
	Publish(ctx context.Context, env realtime.Envelope) error
}

// realtimePusher is the notification use cases' Pusher: it turns an Event into the small payload the web needs
// ({kind, params, url}; the texts are translated by the web) and publishes it for every instance's hub.
type realtimePusher struct{ broadcast publisher }

// eventPayload is the data of a `notification` event.
type eventPayload struct {
	Kind   domain.Kind       `json:"kind"`
	Params map[string]string `json:"params,omitempty"`
	URL    string            `json:"url,omitempty"`
}

// Push publishes the event for the users, a hundred at a time.
func (pusher realtimePusher) Push(ctx context.Context, userIDs []uuid.UUID, e notificationuc.Event) error {
	data, err := json.Marshal(eventPayload{Kind: e.Kind, Params: e.Params, URL: e.URL})
	if err != nil {
		return fmt.Errorf("encode realtime payload: %w", err)
	}
	name := "notification"
	if e.Kind == domain.KindAnnouncement {
		name = "announcement"
	}
	for start := 0; start < len(userIDs); start += pushChunkUsers {
		end := min(start+pushChunkUsers, len(userIDs))
		env := realtime.Envelope{UserIDs: userIDs[start:end], Audience: string(e.Audience), Name: name, Data: data}
		if err := pusher.broadcast.Publish(ctx, env); err != nil {
			return err
		}
	}
	return nil
}

var _ notificationuc.Pusher = realtimePusher{}

// openCommandPusher gives a command (which runs outside the API) the pusher of the realtime stream when Redis is
// configured, and nil (no push) when it is not. The returned function closes the Redis client.
func openCommandPusher(ctx context.Context, cfg config.Config, log *slog.Logger) (notificationuc.Pusher, func(), error) {
	if !cfg.HasRedis() {
		return nil, func() {}, nil
	}
	rdb, err := openRedis(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	broadcaster := redisstore.NewBroadcaster(rdb, cfg.RedisKeyPrefix, log)
	return realtimePusher{broadcast: broadcaster}, func() { _ = rdb.Close() }, nil
}
