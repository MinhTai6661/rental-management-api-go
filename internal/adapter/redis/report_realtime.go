package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"rental-management/internal/adapter/realtime"
	"rental-management/internal/domain/shared"
)

// ReportBroadcaster carries the frames of report conversations between API instances: a frame is published on the
// channel `<prefix>report:<report id>` and every instance, which listens to the pattern `<prefix>report:*`, hands it to
// the sockets it holds on that report. Pub/Sub keeps nothing: a frame nobody is connected for is lost, and a socket
// reloads the conversation over HTTP when it opens.
type ReportBroadcaster struct {
	client *redis.Client
	prefix string
	log    *slog.Logger
}

// NewReportBroadcaster returns the broadcaster; prefix is the Redis key prefix of the application.
func NewReportBroadcaster(client *redis.Client, prefix string, log *slog.Logger) *ReportBroadcaster {
	return &ReportBroadcaster{client: client, prefix: prefix, log: log}
}

func (b *ReportBroadcaster) channel(reportID uuid.UUID) string {
	return b.prefix + "report:" + reportID.String()
}

// Publish sends the frame to every instance.
func (b *ReportBroadcaster) Publish(ctx context.Context, frame realtime.ReportFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode report frame: %w", err)
	}
	if err := b.client.Publish(ctx, b.channel(frame.ReportID), payload).Err(); err != nil {
		return fmt.Errorf("publish report frame: %w: %w", shared.ErrUnavailable, err)
	}
	return nil
}

// Listen hands every frame published for any report to deliver until ctx ends; go-redis reconnects by itself. A payload
// that is not valid JSON is logged and skipped. Run it in a goroutine; it returns nil when ctx ends.
func (b *ReportBroadcaster) Listen(ctx context.Context, deliver func(realtime.ReportFrame)) error {
	subscription := b.client.PSubscribe(ctx, b.prefix+"report:*")
	defer func() { _ = subscription.Close() }()
	if _, err := subscription.Receive(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("subscribe report channels: %w", err)
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
			var frame realtime.ReportFrame
			if err := json.Unmarshal([]byte(message.Payload), &frame); err != nil {
				b.log.Warn("skip a report frame that is not valid", "error", err)
				continue
			}
			deliver(frame)
		}
	}
}
