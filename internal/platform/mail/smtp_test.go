package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is a minimal SMTP server: it accepts one message and keeps its envelope and DATA.
type fakeServer struct {
	addr string
	done chan struct{}

	mu       sync.Mutex
	mailFrom string
	rcptTo   string
	data     string
}

func startFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{addr: ln.Addr().String(), done: make(chan struct{})}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		defer close(s.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		s.serve(conn)
	}()
	return s
}

func (s *fakeServer) serve(conn net.Conn) {
	r := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250 fake") // no STARTTLS, no AUTH: plain delivery
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mu.Lock()
			s.mailFrom = line
			s.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.rcptTo = line
			s.mu.Unlock()
			write("250 ok")
		case upper == "DATA":
			write("354 go ahead")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			s.mu.Lock()
			s.data = data.String()
			s.mu.Unlock()
			write("250 queued")
		case upper == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func TestSMTPSendsAMultipartMessage(t *testing.T) {
	srv := startFakeServer(t)
	date := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	m := &SMTP{Addr: srv.addr, From: "Rental <no-reply@rental.test>", Now: func() time.Time { return date }}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Send(ctx, Message{To: "a@example.vn", Subject: "Xác nhận", Text: "plain body", HTML: "<p>html body</p>"}); err != nil {
		t.Fatal(err)
	}
	<-srv.done

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.Contains(srv.mailFrom, "<no-reply@rental.test>") || !strings.Contains(srv.rcptTo, "<a@example.vn>") {
		t.Errorf("envelope: %q / %q", srv.mailFrom, srv.rcptTo)
	}
	for _, want := range []string{
		"From:", "no-reply@rental.test", "To:", "a@example.vn", "Subject:", "MIME-Version: 1.0",
		"multipart/alternative", "text/plain", "text/html", "plain body", "html body", "Date: Tue, 03 Nov 2026",
	} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message lacks %q:\n%s", want, srv.data)
		}
	}
	if strings.Index(srv.data, "text/plain") > strings.Index(srv.data, "text/html") {
		t.Error("the HTML part must come last")
	}
}

func TestSMTPRefusesHeaderInjectionAndBadAddresses(t *testing.T) {
	m := &SMTP{Addr: "127.0.0.1:1", From: "Rental <no-reply@rental.test>"}
	ctx := context.Background()
	if err := m.Send(ctx, Message{To: "a@example.vn", Subject: "a\r\nBcc: x@evil.test"}); err == nil {
		t.Error("a subject with a line break must be refused")
	}
	if err := m.Send(ctx, Message{To: "not an address", Subject: "s"}); err == nil {
		t.Error("a bad recipient must be refused")
	}
	if err := (&SMTP{Addr: "127.0.0.1:1", From: "nope"}).Send(ctx, Message{To: "a@example.vn", Subject: "s"}); err == nil {
		t.Error("a bad sender must be refused")
	}
}
