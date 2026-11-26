package reporthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	"rental-management/internal/adapter/realtime"
	domain "rental-management/internal/domain/report"
	"rental-management/internal/domain/shared"
	usecase "rental-management/internal/usecase/report"
	"rental-management/pkg/i18n"
	"rental-management/pkg/reqctx"
)

// SocketPath is the route pattern of the conversation socket; the router leaves it out of the request deadline.
const SocketPath = "/v1/reports/:id/ws"

const (
	// retryAfterSeconds is what a client over a socket cap is told to wait.
	retryAfterSeconds = 5
	// outgoingFrames is how many acks and errors may wait for the writer before the socket is closed as stuck.
	outgoingFrames = 16
	// typingGap is the least time between two typing frames that are passed on; the ones in between are dropped.
	typingGap = time.Second
	// closeGrace is how long a socket that was sent a close frame has to answer before it is cut.
	closeGrace = time.Second
	// useCaseTimeout bounds the work a frame starts.
	useCaseTimeout = 15 * time.Second
)

// Tickets redeems the one-time tickets that open a stream or a socket (Redis; the notification realtime issues them).
type Tickets interface {
	Redeem(ctx context.Context, ticket string) (userID, sessionID, role string, err error)
}

// Rooms is the hub of the sockets open on this instance.
type Rooms interface {
	Join(reportID, userID uuid.UUID) (frames <-chan []byte, leave func(), err error)
}

// SocketSettings are the limits and timings of a conversation socket.
type SocketSettings struct {
	// Heartbeat is how often an idle socket is pinged; a socket that answers nothing for twice that long is dropped.
	Heartbeat time.Duration
	// MaxAge is how long a socket lasts before the client must reconnect with a new ticket (so a revoked session loses
	// its sockets within one token life).
	MaxAge time.Duration
	// WriteTimeout bounds every write.
	WriteTimeout time.Duration
	// MaxMessageBytes is the largest frame a client may send.
	MaxMessageBytes int64
	// FramesPerMinute is how many frames a client may send in a minute.
	FramesPerMinute int
	// AllowedOrigins are the web origins that may open a socket from a browser.
	AllowedOrigins []string
}

// SocketHandler serves the live conversation of a report over a WebSocket: the author and the recipient send messages
// and typing, and hear of messages, status changes and typing as they happen. A message goes through the same use case
// as POST /v1/reports/{id}/replies.
type SocketHandler struct {
	uc       *usecase.Reports
	tickets  Tickets
	rooms    Rooms
	settings SocketSettings
	log      *slog.Logger
	now      func() time.Time
	upgrader websocket.Upgrader
}

// NewSocketHandler returns the handler.
func NewSocketHandler(uc *usecase.Reports, tickets Tickets, rooms Rooms, settings SocketSettings, log *slog.Logger) *SocketHandler {
	handler := &SocketHandler{uc: uc, tickets: tickets, rooms: rooms, settings: settings, log: log, now: time.Now}
	handler.upgrader = websocket.Upgrader{
		ReadBufferSize: 1024, WriteBufferSize: 1024, HandshakeTimeout: 10 * time.Second,
		CheckOrigin: func(r *http.Request) bool { return handler.originAllowed(r.Header.Get("Origin")) },
	}
	return handler
}

// originAllowed says whether a browser page of the origin may open a socket: only the configured web origins. A
// request without an Origin header does not come from a browser page (which always sends one), so it is not a
// cross-site attack and the ticket decides.
func (h *SocketHandler) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	origin = strings.TrimRight(strings.ToLower(origin), "/")
	for _, allowed := range h.settings.AllowedOrigins {
		if origin == strings.TrimRight(strings.ToLower(allowed), "/") {
			return true
		}
	}
	return false
}

// Connect answers GET /v1/reports/{id}/ws: the upgrade to a WebSocket. The caller is the owner of the one-time ?ticket
// (a browser cannot send an Authorization header on a WebSocket) or the bearer token. Only a person who may see the
// report joins; one who may not gets the same 404 as GET /v1/reports/{id}. A deleted report can be followed (read
// only): writing in it is refused with an error frame.
func (h *SocketHandler) Connect(c *gin.Context) {
	reportID, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if !h.originAllowed(c.GetHeader("Origin")) {
		httpx.Fail(c, apperr.Forbidden())
		return
	}
	viewer, sessionID, ok := h.socketOwner(c)
	if !ok {
		return
	}
	ctx := reqctx.WithActor(c.Request.Context(), viewer.UserID.String(), viewer.Role, sessionID)
	view, err := h.uc.Get(ctx, viewer, reportID)
	if err != nil {
		httpx.Fail(c, mapReportError(err))
		return
	}
	frames, leave, err := h.rooms.Join(reportID, viewer.UserID)
	if err != nil {
		if errors.Is(err, realtime.ErrTooManyStreams) || errors.Is(err, realtime.ErrReportFull) {
			httpx.Fail(c, apperr.RateLimited(retryAfterSeconds))
			return
		}
		httpx.Fail(c, apperr.Busy(err))
		return
	}
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil { // Upgrade has answered the client
		leave()
		return
	}
	info, _ := reqctx.From(ctx)
	s := &socket{
		h: h, conn: conn, viewer: viewer, reportID: reportID, frames: frames, leave: leave,
		out: make(chan []byte, outgoingFrames), writerDone: make(chan struct{}), localizer: httpx.Localize(c),
		base:   reqctx.With(context.Background(), info),
		budget: frameBudget{limit: h.settings.FramesPerMinute, now: h.now},
	}
	s.run(view)
}

// socketOwner is who the socket is for: the bearer caller, else the owner of the ticket. It answers the request itself
// (and returns false) when neither is valid.
func (h *SocketHandler) socketOwner(c *gin.Context) (domain.Viewer, string, bool) {
	if principal, signedIn := httpx.PrincipalOf(c); signedIn {
		id, err := uuid.Parse(principal.UserID)
		if err != nil {
			httpx.Fail(c, apperr.Unauthenticated())
			return domain.Viewer{}, "", false
		}
		return domain.Viewer{UserID: id, Role: principal.Role}, principal.SessionID, true
	}
	ticket := c.Query("ticket")
	if ticket == "" {
		httpx.Fail(c, apperr.Unauthenticated())
		return domain.Viewer{}, "", false
	}
	userText, sessionID, role, err := h.tickets.Redeem(c.Request.Context(), ticket)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			httpx.Fail(c, apperr.Unauthenticated())
		} else {
			httpx.Fail(c, err)
		}
		return domain.Viewer{}, "", false
	}
	id, err := uuid.Parse(userText)
	if err != nil {
		httpx.Fail(c, apperr.Unauthenticated())
		return domain.Viewer{}, "", false
	}
	return domain.Viewer{UserID: id, Role: role}, sessionID, true
}

// socket is one open connection. Exactly one goroutine reads it (run) and exactly one writes it (writeLoop): the frames
// of the hub, the acks and errors and the pings all go through the writer, and a full queue closes the socket instead of
// blocking whoever sent to it.
type socket struct {
	h        *SocketHandler
	conn     *websocket.Conn
	viewer   domain.Viewer
	reportID uuid.UUID
	frames   <-chan []byte
	leave    func()
	// out carries what only this socket hears: acks and errors.
	out        chan []byte
	writerDone chan struct{}
	localizer  i18n.Localizer
	base       context.Context
	budget     frameBudget

	cancel     context.CancelFunc
	lastTyping time.Time
}

// run reads the socket until it ends, then cleans up: it returns only when the writer is gone too, so a closed socket
// leaves nothing running.
func (s *socket) run(view domain.View) {
	ctx, cancel := context.WithCancel(s.base)
	s.cancel = cancel
	defer func() {
		cancel()
		_ = s.conn.Close()
		s.leave()
		<-s.writerDone
	}()

	s.conn.SetReadLimit(s.h.settings.MaxMessageBytes)
	pongWait := 2 * s.h.settings.Heartbeat
	refresh := func() { _ = s.conn.SetReadDeadline(s.h.now().Add(pongWait)) }
	refresh()
	s.conn.SetPongHandler(func(string) error { refresh(); return nil })
	s.send(readyFrameOf(view))
	go s.writeLoop(ctx)

	for {
		messageType, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		refresh()
		if messageType != websocket.TextMessage {
			s.closeWith(websocket.CloseUnsupportedData, "text frames only")
			return
		}
		if !s.budget.allow() {
			s.closeWith(websocket.ClosePolicyViolation, "too many messages")
			return
		}
		s.handle(ctx, data)
	}
}

// handle answers one client frame.
func (s *socket) handle(ctx context.Context, data []byte) {
	frame, clientID, problem := parseClientFrame(data)
	if problem != nil {
		s.send(errorFrameOf(s.localizer, problem, clientID))
		return
	}
	ctx, cancel := context.WithTimeout(ctx, useCaseTimeout)
	defer cancel()
	switch frame.Type {
	case typeTyping:
		now := s.h.now()
		if now.Sub(s.lastTyping) < typingGap {
			return // the other side needs it once a second at most
		}
		s.lastTyping = now
		if err := s.h.uc.Typing(ctx, s.viewer, s.reportID); err != nil {
			s.send(errorFrameOf(s.localizer, s.appError(err), ""))
		}
	case typeReply:
		replied, err := s.h.uc.Reply(ctx, s.viewer, s.reportID, frame.Body)
		if err != nil {
			s.send(errorFrameOf(s.localizer, s.appError(err), clientID))
			return
		}
		s.send(ackFrameOf(frame.ClientID, replied.Reply.ID))
	}
}

// appError is the API error of an error of the use cases; one it does not know is logged and answered as an internal
// error, like the HTTP API does.
func (s *socket) appError(err error) *apperr.Error {
	mapped := mapReportError(err)
	if ae, ok := apperr.As(mapped); ok {
		return ae
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return apperr.Busy(err)
	}
	s.h.log.Error("report socket", "error", err, "report_id", s.reportID)
	return apperr.Internal(err)
}

// send queues a frame for the writer. A full queue means the client is not reading: the socket is closed, never waited on.
func (s *socket) send(data []byte) {
	select {
	case s.out <- data:
	default:
		s.closeWith(websocket.ClosePolicyViolation, "client too slow")
	}
}

// closeWith sends a close frame (allowed from any goroutine) and cuts the connection if the client does not answer.
func (s *socket) closeWith(code int, reason string) {
	deadline := s.h.now().Add(s.h.settings.WriteTimeout)
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline)
	if s.cancel != nil {
		s.cancel()
	}
	time.AfterFunc(closeGrace, func() { _ = s.conn.Close() })
}

// writeLoop is the only writer of the connection: hub frames, acks and errors, and the ping on every heartbeat. It ends
// with the socket.
func (s *socket) writeLoop(ctx context.Context) {
	defer close(s.writerDone)
	ping := time.NewTicker(s.h.settings.Heartbeat)
	defer ping.Stop()
	expiry := time.NewTimer(s.h.settings.MaxAge)
	defer expiry.Stop()
	for {
		var data []byte
		select {
		case <-ctx.Done():
			return
		case <-expiry.C:
			s.closeWith(websocket.CloseGoingAway, "session expired")
			return
		case <-ping.C:
			deadline := s.h.now().Add(s.h.settings.WriteTimeout)
			if err := s.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				_ = s.conn.Close()
				return
			}
			continue
		case frame, open := <-s.frames:
			if !open { // the server is closing, or this client was too slow for the hub
				s.closeWith(websocket.CloseGoingAway, "closing")
				return
			}
			data = frame
		case data = <-s.out:
		}
		_ = s.conn.SetWriteDeadline(s.h.now().Add(s.h.settings.WriteTimeout))
		if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			_ = s.conn.Close()
			return
		}
	}
}
