package model

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// NewSession is a login session to open, acting in a role.
type NewSession struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Role      string
	UserAgent *string
	IP        *netip.Addr
}

// SessionTouch is what a refresh records on its session: when it was used, the role and where from.
type SessionTouch struct {
	ID        uuid.UUID
	Role      string
	UserAgent *string
	IP        *netip.Addr
}

// NewRefreshToken is a refresh token to store; Hash is the SHA-256 of the token handed to the client.
type NewRefreshToken struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	Hash      []byte
	ParentID  *uuid.UUID
	ExpiresAt time.Time
}

// RefreshToken is a stored refresh token with the state of its session.
type RefreshToken struct {
	ID               uuid.UUID
	SessionID        uuid.UUID
	ExpiresAt        time.Time
	RotatedAt        *time.Time
	UserID           uuid.UUID
	ActiveRole       string
	SessionRevokedAt *time.Time
}

// TokenSession tells which session and user a refresh token belongs to, and whether the session is closed.
type TokenSession struct {
	SessionID uuid.UUID
	UserID    uuid.UUID
	RevokedAt *time.Time
}

// NewVerificationToken is an email verification token to store (its hash only).
type NewVerificationToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Hash      []byte
	ExpiresAt time.Time
}

// VerificationToken is a stored email verification token.
type VerificationToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	UsedAt    *time.Time
}
