package reqctx

import (
	"context"
	"testing"
)

func TestWithActorKeepsTheRequestFacts(t *testing.T) {
	ctx := With(context.Background(), Info{RequestID: "r1", IP: "1.2.3.4", UserAgent: "ua"})
	ctx = WithActor(ctx, "u1", "admin", "s1")
	got, ok := From(ctx)
	if !ok || got.RequestID != "r1" || got.IP != "1.2.3.4" || got.ActorID != "u1" || got.Role != "admin" || got.SessionID != "s1" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := From(context.Background()); ok {
		t.Fatal("a bare context carries nothing")
	}
}
