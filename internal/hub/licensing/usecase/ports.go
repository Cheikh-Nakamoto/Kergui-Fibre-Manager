// Package usecase holds the licensing module's application logic (RFC-006):
// the WhatsApp sign-in, the trial, the NabooPay checkout and its webhook,
// crediting a pass, box transfers, the entitlement token and the expiry
// reminder. The rules themselves live in the domain package; this layer
// decides where time, randomness and storage come from.
package usecase

import (
	"context"
	"errors"
	"time"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// Errors the handlers map to status codes. The domain's own errors (trial
// used, transfer limit, unknown pass…) pass through unchanged.
var (
	ErrNotFound = errors.New("licensing: not found")
	// ErrOTPRateLimited is one code too many for a number: wait.
	ErrOTPRateLimited = errors.New("licensing: too many codes requested for this number")
	// ErrOTPInvalid is a wrong code that still has guesses left: type it
	// again.
	//
	// The three refusals are told apart so the person knows what to do; none
	// says anything about the code's digits. That a number has a pending code
	// is not a secret either: anyone can ask for one.
	ErrOTPInvalid = errors.New("licensing: the code is wrong")
	// ErrOTPExpired is a code past its ten minutes, already used, or never
	// sent: ask for a new one.
	ErrOTPExpired = errors.New("licensing: the code has expired, ask for a new one")
	// ErrOTPLocked is a code that had its OTPMaxAttempts guesses: ask for a
	// new one.
	ErrOTPLocked = errors.New("licensing: too many wrong attempts, ask for a new code")
	// ErrUnauthenticated is a missing, unknown, expired or revoked session.
	ErrUnauthenticated = errors.New("licensing: sign in again")
	// ErrPaymentsOff is a checkout on a hub with no payment provider.
	ErrPaymentsOff = errors.New("licensing: payments are not configured on this hub")
	// ErrTargetActive refuses a transfer onto a box where the account
	// already runs a pass: merging two passes is a support case.
	ErrTargetActive = errors.New("licensing: this account already has an active pass on the target box")
	// ErrPaymentMismatch is a provider reference already recorded for a
	// different account, box or pass.
	ErrPaymentMismatch = errors.New("licensing: this payment reference belongs to another purchase")
	// ErrTrialRace is the database refusing a second trial that the
	// up-front check missed (two requests at once).
	ErrTrialRace = errors.New("licensing: the trial was already used")
)

// Clock is the module's time source.
type Clock func() time.Time

// Logger is the logging port.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Sender delivers a WhatsApp text. The alerts module's Notifier (Evolution
// Go, or the console in a pilot) satisfies it as is.
type Sender interface {
	Send(ctx context.Context, to alerts.Phone, msg alerts.OutboundMessage) (string, error)
}

// Account is a verified phone number.
type Account struct {
	ID        domain.AccountID
	Phone     domain.Phone
	CreatedAt time.Time
}

// Challenge is one one-time code sent to a number. Only its hash is kept.
type Challenge struct {
	ID         string
	Phone      domain.Phone
	CodeHash   []byte
	ExpiresAt  time.Time
	Attempts   int
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

// Session is one signed-in phone of an account (the household's several
// phones each hold one). Only the hash of its token is kept.
type Session struct {
	Account   domain.AccountID
	Device    string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// PaymentStatus is where a payment stands on the hub's side.
type PaymentStatus string

const (
	PaymentPending   PaymentStatus = "pending"
	PaymentCredited  PaymentStatus = "credited"
	PaymentFailed    PaymentStatus = "failed"
	PaymentCancelled PaymentStatus = "cancelled"
)

// Payment providers.
const (
	ProviderNabooPay = "naboopay"
	// ProviderPlay is Google Play Billing: one payment per purchase token
	// and expiry, so each renewal is credited once.
	ProviderPlay = "play"
)

// Payment is one purchase of a pass, idempotent by (Provider, Reference):
// the NabooPay order id, or the Play purchase token and the expiry it paid
// up to (PlayReference). For Play, PassCode is the Play product id.
type Payment struct {
	ID          string
	Provider    string
	Reference   string
	Account     domain.AccountID
	BoxID       domain.BoxID
	PassCode    string
	AmountXOF   int64
	Status      PaymentStatus
	CheckoutURL string
	CreatedAt   time.Time
	CreditedAt  *time.Time
}

// Trial records who had the 7-day Max trial: once per number, once per box.
type Trial struct {
	Phone     domain.Phone
	BoxID     domain.BoxID
	Account   domain.AccountID
	StartedAt time.Time
}

// Transfer is one move of a pass to another box.
type Transfer struct {
	Account domain.AccountID
	From    domain.BoxID
	To      domain.BoxID
	At      time.Time
}

// Store is the module's storage. Every write that reads a subscription
// first runs inside InAccount, which serialises the account's writes, so a
// rule checked in Go still holds when the row is written.
type Store interface {
	// InAccount runs fn in one transaction holding the account's lock.
	InAccount(ctx context.Context, id domain.AccountID, fn func(ctx context.Context) error) error

	EnsureAccount(ctx context.Context, a Account) (Account, error)
	AccountByID(ctx context.Context, id domain.AccountID) (Account, error)
	AccountByPhone(ctx context.Context, p domain.Phone) (Account, error)

	CreateChallenge(ctx context.Context, c Challenge) error
	// ChallengesSince counts the codes sent to a number since a time.
	ChallengesSince(ctx context.Context, p domain.Phone, since time.Time) (n int, latest time.Time, err error)
	LatestChallenge(ctx context.Context, p domain.Phone) (Challenge, error)
	// AddAttempt counts one try, unless max is reached; false means locked.
	AddAttempt(ctx context.Context, id string, max int) (bool, error)
	// ConsumeChallenge spends a code once; false means it was already spent.
	ConsumeChallenge(ctx context.Context, id string, at time.Time) (bool, error)

	CreateSession(ctx context.Context, tokenHash []byte, s Session) error
	SessionByHash(ctx context.Context, tokenHash []byte) (Session, error)
	RevokeSession(ctx context.Context, tokenHash []byte, at time.Time) error

	// Subscription returns ErrNotFound when the account has none on the box.
	Subscription(ctx context.Context, a domain.AccountID, b domain.BoxID) (domain.Subscription, error)
	SaveSubscription(ctx context.Context, s domain.Subscription) error
	DeleteSubscription(ctx context.Context, a domain.AccountID, b domain.BoxID) error
	ListSubscriptions(ctx context.Context, a domain.AccountID) ([]domain.Subscription, error)

	TrialUsed(ctx context.Context, p domain.Phone, b domain.BoxID) (phoneUsed, boxUsed bool, err error)
	// RecordTrial returns ErrTrialRace when the number or the box is taken.
	RecordTrial(ctx context.Context, t Trial) error

	TransfersSince(ctx context.Context, a domain.AccountID, since time.Time) ([]time.Time, error)
	RecordTransfer(ctx context.Context, t Transfer) error

	CreatePayment(ctx context.Context, p Payment) error
	PaymentByRef(ctx context.Context, provider, ref string) (Payment, error)
	SetPaymentStatus(ctx context.Context, id string, st PaymentStatus, at time.Time) error
	PendingPayments(ctx context.Context, provider string, since time.Time, limit int) ([]Payment, error)

	// DueReminders lists the running subscriptions ending at or before
	// until whose current expiry was not announced yet.
	DueReminders(ctx context.Context, now, until time.Time, limit int) ([]domain.Subscription, error)
	// ClaimReminder marks the reminder of s's current expiry as sent and
	// logs it; false means someone else claimed it or the expiry moved.
	ClaimReminder(ctx context.Context, s domain.Subscription, at time.Time) (bool, error)
	// ReleaseReminder undoes a claim whose message could not be sent.
	ReleaseReminder(ctx context.Context, s domain.Subscription) error

	PlayStore
}
