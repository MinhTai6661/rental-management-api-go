// Package mail sends the emails of the application. Mailer is the port; Log, Recorder and SMTP implement it.
package mail

import (
	"context"
	"log/slog"
	"sync"
)

// Message is one email with a plain-text and an HTML body (clients show whichever they prefer).
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends a message.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Log is a Mailer that only writes the message to the log. It is used when no SMTP server is configured, so
// a developer can still read the link of a verification email.
type Log struct{ log *slog.Logger }

// NewLog returns a Mailer that logs instead of sending.
func NewLog(log *slog.Logger) *Log { return &Log{log: log} }

// Send logs the message, including the text body.
func (l *Log) Send(_ context.Context, msg Message) error {
	l.log.Info("email not sent (SMTP_HOST is not set)", "to", msg.To, "subject", msg.Subject, "text", msg.Text)
	return nil
}

// Recorder is a Mailer for tests: it keeps what was sent.
type Recorder struct {
	mu   sync.Mutex
	sent []Message
}

// Send records the message.
func (r *Recorder) Send(_ context.Context, msg Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return nil
}

// Sent returns a copy of the recorded messages.
func (r *Recorder) Sent() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.sent...)
}
