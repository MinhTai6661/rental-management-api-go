// Package reqctx carries the facts of the request being served (who is acting, in which role and session, from where)
// in a context.Context, so the layers below HTTP, the audit trail first of all, can describe an action without
// importing anything HTTP. The HTTP middleware fills it in; jobs and commands leave it empty.
package reqctx

import "context"

// Info is what is known about the request. Every field may be empty.
type Info struct {
	// RequestID is the id of the request (the caller's X-Request-Id or one made for it).
	RequestID string
	// IP and UserAgent are where the request came from.
	IP        string
	UserAgent string
	// ActorID, Role and SessionID are the signed in caller, the role the request acts in and the login session.
	ActorID   string
	Role      string
	SessionID string
}

type key struct{}

// With returns ctx carrying info.
func With(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, key{}, info)
}

// From returns the info of ctx; ok is false when ctx does not come from a request.
func From(ctx context.Context) (Info, bool) {
	info, ok := ctx.Value(key{}).(Info)
	return info, ok
}

// WithActor returns ctx with the signed in caller added to what it already carries.
func WithActor(ctx context.Context, actorID, role, sessionID string) context.Context {
	info, _ := From(ctx)
	info.ActorID, info.Role, info.SessionID = actorID, role, sessionID
	return With(ctx, info)
}
