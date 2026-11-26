package reporthttp

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/report"
	"rental-management/pkg/i18n"
)

// ProtocolVersion is the version of the conversation protocol, told to the client in the `ready` frame. A change that
// an old client cannot follow gets a new version.
const ProtocolVersion = 1

// The types of the frames a client sends.
const (
	typeReply  = "reply"
	typeTyping = "typing"
)

// maxBodyChars is the longest message, as for POST /v1/reports/{id}/replies.
const maxBodyChars = 5000

// clientFrame is a frame as the client sends it: {"type":"reply","client_id":"<uuid>","body":"..."} or {"type":"typing"}.
type clientFrame struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id"`
	Body     string `json:"body"`
}

// parsedFrame is a valid client frame.
type parsedFrame struct {
	Type     string
	ClientID uuid.UUID
	Body     string
}

// parseClientFrame reads one text frame. The error is the one to answer with (the same codes as the HTTP API), and the
// client id (when it could be read) so the client can tell which of its messages was refused.
func parseClientFrame(data []byte) (parsedFrame, string, *apperr.Error) {
	var raw clientFrame
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raw); err != nil {
		return parsedFrame{}, "", apperr.Malformed()
	}
	switch raw.Type {
	case typeTyping:
		return parsedFrame{Type: typeTyping}, "", nil
	case typeReply:
	default:
		return parsedFrame{}, raw.ClientID, apperr.Invalid(apperr.Field{Name: "type", Rule: apperr.RuleOneOf, Params: map[string]string{"values": "reply, typing"}})
	}
	clientID, err := uuid.Parse(raw.ClientID)
	if err != nil {
		return parsedFrame{}, "", apperr.Invalid(apperr.Field{Name: "client_id", Rule: apperr.RuleUUID})
	}
	body := strings.TrimSpace(raw.Body)
	switch {
	case body == "":
		return parsedFrame{}, raw.ClientID, apperr.Invalid(apperr.Field{Name: "body", Rule: apperr.RuleRequired})
	case utf8.RuneCountInString(body) > maxBodyChars:
		return parsedFrame{}, raw.ClientID, apperr.Invalid(apperr.Field{Name: "body", Rule: apperr.RuleMaxString, Params: map[string]string{"max": "5000"}})
	}
	return parsedFrame{Type: typeReply, ClientID: clientID, Body: body}, raw.ClientID, nil
}

// The frames the server sends. Each is one JSON text frame.

type (
	readyFrame struct {
		Type     string        `json:"type"`
		Version  int           `json:"v"`
		ReportID uuid.UUID     `json:"report_id"`
		Status   domain.Status `json:"status"`
		Deleted  bool          `json:"deleted"`
	}
	replyFrame struct {
		Type  string    `json:"type"`
		Reply ReplyView `json:"reply"`
	}
	statusFrame struct {
		Type   string        `json:"type"`
		Status domain.Status `json:"status"`
	}
	typingFrame struct {
		Type   string    `json:"type"`
		UserID uuid.UUID `json:"user_id"`
	}
	ackFrame struct {
		Type     string    `json:"type"`
		ClientID uuid.UUID `json:"client_id"`
		ReplyID  uuid.UUID `json:"reply_id"`
	}
	errorFrame struct {
		Type     string              `json:"type"`
		Code     string              `json:"code"`
		Message  string              `json:"message"`
		Fields   map[string][]string `json:"fields,omitempty"`
		ClientID string              `json:"client_id,omitempty"`
	}
)

func mustJSON(frame any) []byte {
	data, err := json.Marshal(frame)
	if err != nil { // the frames are plain structs: this cannot happen
		panic("encode report frame: " + err.Error())
	}
	return data
}

func readyFrameOf(v domain.View) []byte {
	return mustJSON(readyFrame{Type: "ready", Version: ProtocolVersion, ReportID: v.ID, Status: v.Status, Deleted: v.Deleted()})
}

func ackFrameOf(clientID, replyID uuid.UUID) []byte {
	return mustJSON(ackFrame{Type: "ack", ClientID: clientID, ReplyID: replyID})
}

// errorFrameOf is the error to answer with, translated like the HTTP API's.
func errorFrameOf(l i18n.Localizer, err *apperr.Error, clientID string) []byte {
	frame := errorFrame{Type: "error", Code: err.Code, Message: l.T("errors."+err.Code, err.Params), ClientID: clientID}
	if len(err.Fields) > 0 {
		frame.Fields = make(map[string][]string, len(err.Fields))
		for _, f := range err.Fields {
			params := map[string]string{"attribute": strings.ReplaceAll(f.Name, "_", " ")}
			for k, v := range f.Params {
				params[k] = v
			}
			frame.Fields[f.Name] = append(frame.Fields[f.Name], l.T("validation."+f.Rule, params))
		}
	}
	return mustJSON(frame)
}

// frameBudget is how many frames a socket may send in a minute; past it the socket is closed (policy violation). It is
// a fixed one-minute window, which is plenty for a person typing and stops a script.
type frameBudget struct {
	limit       int
	now         func() time.Time
	windowStart time.Time
	count       int
}

// allow counts a frame and says whether it is within the budget.
func (b *frameBudget) allow() bool {
	now := b.now()
	if now.Sub(b.windowStart) >= time.Minute {
		b.windowStart, b.count = now, 0
	}
	b.count++
	return b.count <= b.limit
}
