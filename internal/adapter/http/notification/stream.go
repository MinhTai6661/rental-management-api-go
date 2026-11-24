package notificationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	"rental-management/internal/adapter/realtime"
	"rental-management/internal/domain/shared"
)

// StreamPath is the full path of the event stream; the router leaves it out of the request deadline.
const StreamPath = "/v1/realtime/stream"

// retryAfterSeconds is what a client over the stream cap is told to wait.
const retryAfterSeconds = 5

// Tickets issues and redeems the one-time tickets that open a stream (Redis).
type Tickets interface {
	Issue(ctx context.Context, userID, sessionID, role string) (string, error)
	Redeem(ctx context.Context, ticket string) (userID, sessionID, role string, err error)
}

// Streams is the hub of the streams open on this instance.
type Streams interface {
	Subscribe(userID uuid.UUID, role string) (events <-chan realtime.Frame, cancel func(), err error)
}

// StreamSettings are the timings of a stream.
type StreamSettings struct {
	TicketTTL time.Duration
	Heartbeat time.Duration
	MaxAge    time.Duration
}

// StreamHandler serves the realtime routes: the ticket and the event stream.
type StreamHandler struct {
	tickets  Tickets
	streams  Streams
	settings StreamSettings
}

// NewStreamHandler returns the handler.
func NewStreamHandler(tickets Tickets, streams Streams, settings StreamSettings) *StreamHandler {
	return &StreamHandler{tickets: tickets, streams: streams, settings: settings}
}

type ticketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
	StreamURL string `json:"stream_url"`
}

// Ticket answers POST /v1/realtime/ticket: a ticket for the caller, in the role they act in now, good for one stream.
func (handler *StreamHandler) Ticket(ginCtx *gin.Context) {
	principal, ok := httpx.PrincipalOf(ginCtx)
	if !ok {
		httpx.Fail(ginCtx, apperr.Unauthenticated())
		return
	}
	ticket, err := handler.tickets.Issue(ginCtx.Request.Context(), principal.UserID, principal.SessionID, principal.Role)
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	ginCtx.JSON(http.StatusCreated, gin.H{"data": ticketResponse{
		Ticket:    ticket,
		ExpiresIn: int(handler.settings.TicketTTL / time.Second),
		StreamURL: StreamPath + "?ticket=" + ticket,
	}})
}

// Stream answers GET /v1/realtime/stream as text/event-stream. The caller is the bearer token when the request has
// one (fetch-based clients) or else the one-time ?ticket (EventSource). The stream sends `ready` once, then the
// events for the caller, a `: ping` comment on every heartbeat, and ends with `expired` after the maximum age so
// that a revoked session loses its stream within one token life. Nothing is replayed: the client reloads its inbox on
// `ready`.
func (handler *StreamHandler) Stream(ginCtx *gin.Context) {
	userID, role, ok := handler.streamOwner(ginCtx)
	if !ok {
		return
	}
	events, cancel, err := handler.streams.Subscribe(userID, role)
	if err != nil {
		if errors.Is(err, realtime.ErrTooManyStreams) {
			httpx.Fail(ginCtx, apperr.RateLimited(retryAfterSeconds))
			return
		}
		httpx.Fail(ginCtx, apperr.Busy(err))
		return
	}
	defer cancel()

	header := ginCtx.Writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	// The server's write deadline would cut a stream that is quiet for a while; the stream has its own lifetime.
	if err := http.NewResponseController(ginCtx.Writer).SetWriteDeadline(time.Time{}); err != nil {
		httpx.Log(ginCtx).Warn("lift the write deadline of a stream", "error", err)
	}
	ginCtx.Status(http.StatusOK)
	if !writeEvent(ginCtx, "ready", "{}") {
		return
	}

	heartbeat := time.NewTicker(handler.settings.Heartbeat)
	defer heartbeat.Stop()
	expiry := time.NewTimer(handler.settings.MaxAge)
	defer expiry.Stop()
	for {
		select {
		case <-ginCtx.Request.Context().Done():
			return
		case <-expiry.C:
			writeEvent(ginCtx, "expired", "{}")
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(ginCtx.Writer, ": ping\n\n"); err != nil {
				return
			}
			ginCtx.Writer.Flush()
		case frame, open := <-events:
			if !open {
				return
			}
			if !writeEvent(ginCtx, frame.Name, string(frame.Data)) {
				return
			}
		}
	}
}

// streamOwner is who the stream is for: the bearer caller, else the owner of the ticket. It answers the request
// itself (and returns false) when neither is valid.
func (handler *StreamHandler) streamOwner(ginCtx *gin.Context) (uuid.UUID, string, bool) {
	if principal, signedIn := httpx.PrincipalOf(ginCtx); signedIn {
		userID, err := uuid.Parse(principal.UserID)
		if err != nil {
			httpx.Fail(ginCtx, apperr.Unauthenticated())
			return uuid.Nil, "", false
		}
		return userID, principal.Role, true
	}
	ticket := ginCtx.Query("ticket")
	if ticket == "" {
		httpx.Fail(ginCtx, apperr.Unauthenticated())
		return uuid.Nil, "", false
	}
	userIDText, _, role, err := handler.tickets.Redeem(ginCtx.Request.Context(), ticket)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			httpx.Fail(ginCtx, apperr.Unauthenticated())
		} else {
			httpx.Fail(ginCtx, err)
		}
		return uuid.Nil, "", false
	}
	userID, err := uuid.Parse(userIDText)
	if err != nil {
		httpx.Fail(ginCtx, apperr.Unauthenticated())
		return uuid.Nil, "", false
	}
	return userID, role, true
}

// writeEvent writes one event and flushes it; false means the client is gone.
func writeEvent(ginCtx *gin.Context, name, data string) bool {
	if _, err := fmt.Fprintf(ginCtx.Writer, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return false
	}
	ginCtx.Writer.Flush()
	return true
}
