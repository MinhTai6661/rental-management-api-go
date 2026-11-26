package realtime

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/google/uuid"
)

// ErrReportFull is returned by Join when the report already holds the most sockets one instance allows.
var ErrReportFull = errors.New("too many sockets on this report")

// ReportFrame is what travels through Redis for a conversation: one ready-made JSON frame for the sockets open on a
// report. ExceptUser, when set, is a person whose sockets must not get it (the one who is typing).
type ReportFrame struct {
	ReportID   uuid.UUID       `json:"report_id"`
	ExceptUser *uuid.UUID      `json:"except_user,omitempty"`
	Data       json.RawMessage `json:"data"`
}

// reportMember is one open socket of a conversation.
type reportMember struct {
	userID uuid.UUID
	frames chan []byte
}

// ReportHub holds the sockets open on this instance, by report. It knows nothing of WebSocket: a socket joins to get a
// channel of frames and gives it back with leave. Delivery never blocks: a socket that cannot keep up has its channel
// closed (its writer then closes the socket, and the client reconnects and reloads the conversation).
type ReportHub struct {
	maxPerUser, maxPerReport int
	bufferedFrames           int

	mu      sync.Mutex
	reports map[uuid.UUID]map[*reportMember]struct{}
	perUser map[uuid.UUID]int
	closed  bool
}

// NewReportHub returns a hub that lets a user hold at most maxPerUser sockets and a report at most maxPerReport, and
// that lets bufferedFrames frames wait for a slow socket before it is dropped.
func NewReportHub(maxPerUser, maxPerReport, bufferedFrames int) *ReportHub {
	return &ReportHub{
		maxPerUser: maxPerUser, maxPerReport: maxPerReport, bufferedFrames: bufferedFrames,
		reports: map[uuid.UUID]map[*reportMember]struct{}{}, perUser: map[uuid.UUID]int{},
	}
}

// Join opens a socket of the user on the report. The channel is closed when leave is called, when the hub is closed, or
// when the consumer is too slow. leave is idempotent.
func (hub *ReportHub) Join(reportID, userID uuid.UUID) (frames <-chan []byte, leave func(), err error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return nil, nil, ErrClosed
	}
	if hub.perUser[userID] >= hub.maxPerUser {
		return nil, nil, ErrTooManyStreams
	}
	if len(hub.reports[reportID]) >= hub.maxPerReport {
		return nil, nil, ErrReportFull
	}
	member := &reportMember{userID: userID, frames: make(chan []byte, hub.bufferedFrames)}
	if hub.reports[reportID] == nil {
		hub.reports[reportID] = map[*reportMember]struct{}{}
	}
	hub.reports[reportID][member] = struct{}{}
	hub.perUser[userID]++
	return member.frames, func() {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		hub.drop(reportID, member)
	}, nil
}

// Deliver hands the frame to every socket open on the report (but the one of ExceptUser) without ever blocking.
func (hub *ReportHub) Deliver(frame ReportFrame) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for member := range hub.reports[frame.ReportID] {
		if frame.ExceptUser != nil && member.userID == *frame.ExceptUser {
			continue
		}
		select {
		case member.frames <- frame.Data:
		default:
			hub.drop(frame.ReportID, member)
		}
	}
}

// Open is how many sockets are open on this instance.
func (hub *ReportHub) Open() int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	total := 0
	for _, open := range hub.reports {
		total += len(open)
	}
	return total
}

// Close ends every socket and refuses new ones. It is safe to call more than once.
func (hub *ReportHub) Close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.closed = true
	for reportID, open := range hub.reports {
		for member := range open {
			hub.drop(reportID, member)
		}
	}
}

// drop removes the member and closes its channel once; the caller holds the lock.
func (hub *ReportHub) drop(reportID uuid.UUID, member *reportMember) {
	open, ok := hub.reports[reportID]
	if !ok {
		return
	}
	if _, found := open[member]; !found {
		return
	}
	delete(open, member)
	if len(open) == 0 {
		delete(hub.reports, reportID)
	}
	if hub.perUser[member.userID]--; hub.perUser[member.userID] <= 0 {
		delete(hub.perUser, member.userID)
	}
	close(member.frames)
}
