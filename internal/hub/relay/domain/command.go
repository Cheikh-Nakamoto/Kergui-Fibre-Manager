package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Status is where a command is in its short life.
type Status string

const (
	// StatusQueued is waiting for the lease holder to pick it up.
	StatusQueued Status = "queued"
	// StatusDelivered has been handed to a relay.
	StatusDelivered Status = "delivered"
	// StatusDone carries the relay's signed result, whatever the box said.
	StatusDone Status = "done"
	// StatusExpired was never picked up in time.
	StatusExpired Status = "expired"
)

// Errors the handlers map to status codes.
var (
	ErrNoCommand      = errors.New("relay: no such command")
	ErrNotLeaseHolder = errors.New("relay: this relay does not hold the lease")
	ErrStaleEpoch     = errors.New("relay: the lease has changed hands since this command was delivered")
	ErrNoSnapshot     = errors.New("relay: no snapshot has been pushed yet")
)

// Command is one signed order on its way to the box, as the hub stores it.
// Envelope and Body are the bytes the owner signed; the hub never edits
// them, and the relay verifies them against the trust bundle.
type Command struct {
	ID           string
	TenantID     string
	IssuerUserID string
	Envelope     json.RawMessage // trust.SignedCommand
	Body         []byte
	Method       string
	Path         string
	Status       Status
	Epoch        int64
	AgentID      string
	Result       json.RawMessage // trust.SignedResult
	CreatedAt    time.Time
	ExpiresAt    time.Time
	DeliveredAt  *time.Time
	DoneAt       *time.Time
}

// Snapshot is state a relay pushed, signed with its key. The app verifies
// it, so the hub cannot invent a device list.
type Snapshot struct {
	TenantID string
	Kind     string
	AgentID  string
	Payload  json.RawMessage // trust.SignedSnapshot
	AsOf     time.Time
}

// LeaseRepository stores one lease per household.
type LeaseRepository interface {
	// Heartbeat records a presence and settles the lease in one
	// transaction, applying decide to the row it locked. Locking is what
	// keeps two relays from both being told "active".
	Heartbeat(ctx context.Context, tenantID string, p Presence, r Report, now time.Time,
		decide func(cur Lease, me Presence, others []Presence) Outcome) (Outcome, error)
	Current(ctx context.Context, tenantID string) (Lease, error)
}

// CommandRepository stores the command queue.
type CommandRepository interface {
	Enqueue(ctx context.Context, c *Command) error
	// Claim hands the oldest due command to the lease holder, or returns
	// ErrNoCommand. It skips locked rows, so two hub instances never hand
	// out the same one.
	Claim(ctx context.Context, tenantID, agentID string, epoch int64, now time.Time) (*Command, error)
	Complete(ctx context.Context, tenantID, id, agentID string, epoch int64, result json.RawMessage, now time.Time) error
	ByID(ctx context.Context, tenantID, id string) (*Command, error)
	// Expire marks commands nobody picked up, so a household sees why
	// nothing happened.
	Expire(ctx context.Context, now time.Time) (int, error)
}

// SnapshotRepository stores the last state each relay pushed.
type SnapshotRepository interface {
	Put(ctx context.Context, s Snapshot) error
	Latest(ctx context.Context, tenantID, kind string) (Snapshot, error)
}
