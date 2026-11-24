// Package realtime is the in-memory hub that hands server-sent events to the streams open on this instance. The
// fan-out between instances is Redis Pub/Sub (internal/adapter/redis/realtime.go); this package knows nothing of it.
package realtime

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/google/uuid"
)

// bufferedFrames is how many frames may wait for a slow stream before it is dropped.
const bufferedFrames = 16

// ErrTooManyStreams is returned by Subscribe when the user already holds the most streams one instance allows.
var ErrTooManyStreams = errors.New("too many open streams")

// ErrClosed is returned by Subscribe once the hub is closed (the process is shutting down).
var ErrClosed = errors.New("realtime hub closed")

// Frame is one server-sent event: its name and its JSON data (a single line).
type Frame struct {
	Name string
	Data []byte
}

// Envelope is what travels through Redis: one event for up to a few hundred users. A set Audience reaches only the
// streams acting in that role (the inbox is split by role); an empty one reaches every stream of the users.
type Envelope struct {
	UserIDs  []uuid.UUID     `json:"user_ids"`
	Audience string          `json:"audience,omitempty"`
	Name     string          `json:"name"`
	Data     json.RawMessage `json:"data"`
}

// roleScoped are the roles an Envelope audience can name; a stream acting in any other role (staff) gets every event.
var roleScoped = map[string]struct{}{"tenant": {}, "landlord": {}, "manager": {}}

type stream struct {
	role   string
	frames chan Frame
}

// Hub holds the streams open on this instance, by user.
type Hub struct {
	maxPerUser int

	mu      sync.Mutex
	streams map[uuid.UUID]map[*stream]struct{}
	closed  bool
}

// NewHub returns a hub that lets a user hold at most maxPerUser streams.
func NewHub(maxPerUser int) *Hub {
	return &Hub{maxPerUser: maxPerUser, streams: map[uuid.UUID]map[*stream]struct{}{}}
}

// Subscribe opens a stream for the user acting in role. The channel is closed when the stream is cancelled, when the
// hub is closed, or when the consumer is too slow (the client then reconnects and reloads). cancel is idempotent.
func (hub *Hub) Subscribe(userID uuid.UUID, role string) (events <-chan Frame, cancel func(), err error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return nil, nil, ErrClosed
	}
	if len(hub.streams[userID]) >= hub.maxPerUser {
		return nil, nil, ErrTooManyStreams
	}
	open := &stream{role: role, frames: make(chan Frame, bufferedFrames)}
	if hub.streams[userID] == nil {
		hub.streams[userID] = map[*stream]struct{}{}
	}
	hub.streams[userID][open] = struct{}{}
	return open.frames, func() {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		hub.drop(userID, open)
	}, nil
}

// Deliver hands the event to every matching local stream without ever blocking: a stream whose buffer is full is
// closed instead (it missed an event, so it must reload anyway).
func (hub *Hub) Deliver(env Envelope) {
	frame := Frame{Name: env.Name, Data: env.Data}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, userID := range env.UserIDs {
		for open := range hub.streams[userID] {
			if !reaches(env.Audience, open.role) {
				continue
			}
			select {
			case open.frames <- frame:
			default:
				hub.drop(userID, open)
			}
		}
	}
}

// Close ends every stream and refuses new ones. It is safe to call more than once.
func (hub *Hub) Close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.closed = true
	for userID, open := range hub.streams {
		for one := range open {
			hub.drop(userID, one)
		}
	}
}

// drop removes the stream and closes its channel once; the caller holds the lock.
func (hub *Hub) drop(userID uuid.UUID, open *stream) {
	streamsOfUser, ok := hub.streams[userID]
	if !ok {
		return
	}
	if _, found := streamsOfUser[open]; !found {
		return
	}
	delete(streamsOfUser, open)
	if len(streamsOfUser) == 0 {
		delete(hub.streams, userID)
	}
	close(open.frames)
}

// reaches reports whether an event for the audience is meant for a stream acting in role.
func reaches(audience, role string) bool {
	if audience == "" {
		return true
	}
	if _, scoped := roleScoped[role]; !scoped {
		return true
	}
	return audience == role
}
