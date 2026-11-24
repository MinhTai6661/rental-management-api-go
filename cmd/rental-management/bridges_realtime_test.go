package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"rental-management/internal/adapter/realtime"
	domain "rental-management/internal/domain/notification"
	notificationuc "rental-management/internal/usecase/notification"
)

type recordingPublisher struct{ sent []realtime.Envelope }

func (r *recordingPublisher) Publish(_ context.Context, env realtime.Envelope) error {
	r.sent = append(r.sent, env)
	return nil
}

func TestPusherSendsASmallPayloadInChunksOfAHundred(t *testing.T) {
	recorder := &recordingPublisher{}
	users := make([]uuid.UUID, 250)
	for i := range users {
		users[i] = uuid.New()
	}
	event := notificationuc.Event{Kind: domain.InvoiceSent, Params: map[string]string{"room": "A1"}, URL: "/invoices/1", Audience: domain.AudienceTenant}
	if err := (realtimePusher{broadcast: recorder}).Push(context.Background(), users, event); err != nil {
		t.Fatal(err)
	}
	if len(recorder.sent) != 3 || len(recorder.sent[0].UserIDs) != 100 || len(recorder.sent[2].UserIDs) != 50 {
		t.Fatalf("chunks: %d", len(recorder.sent))
	}
	first := recorder.sent[0]
	if first.Name != "notification" || first.Audience != "tenant" {
		t.Fatalf("envelope %+v", first)
	}
	var payload map[string]any
	if err := json.Unmarshal(first.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != "invoice_sent" || payload["url"] != "/invoices/1" || len(payload) != 3 {
		t.Fatalf("payload %v", payload)
	}
}

func TestPusherNamesAnnouncementsApart(t *testing.T) {
	recorder := &recordingPublisher{}
	event := notificationuc.Event{Kind: domain.KindAnnouncement, URL: "/notifications"}
	if err := (realtimePusher{broadcast: recorder}).Push(context.Background(), []uuid.UUID{uuid.New()}, event); err != nil {
		t.Fatal(err)
	}
	if recorder.sent[0].Name != "announcement" || string(recorder.sent[0].Data) != `{"kind":"announcement","url":"/notifications"}` {
		t.Fatalf("%+v %s", recorder.sent[0], recorder.sent[0].Data)
	}
}
