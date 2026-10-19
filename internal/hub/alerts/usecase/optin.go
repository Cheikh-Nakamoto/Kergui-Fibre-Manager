// Package usecase holds the alerts module's application logic: the WhatsApp
// opt-in, and turning a closed inventory window into messages.
package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Opt-in parameters.
const (
	// CodeTTL is how long a pairing code stays usable. The owner shows a
	// link or a QR and the person writes right away; a code that outlives
	// the screen it was shown on is a code that leaks.
	CodeTTL = 15 * time.Minute
	// codeBytes gives 40 bits of entropy, written as 8 base32 characters:
	// short enough to type on a phone, far too large to guess inside the
	// quarter of an hour it lives.
	codeBytes = 5
	// Keyword is what the wa.me link pre-fills, so the hub can tell an
	// opt-in from any other message.
	Keyword = "KERGUI"
)

// codeAlphabet avoids the characters people mistype (0/O, 1/I).
var codeAlphabet = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// Clock is the module's time source; tests inject one.
type Clock func() time.Time

// OptIn issues the codes and verifies the messages that redeem them.
//
// WhatsApp requires the person to write first, which is also the design:
// the hub never holds a personal WhatsApp session, and a number only ever
// arrives because its owner sent a message from it (RFC-005).
type OptIn struct {
	Repo domain.Repository
	// Number is the household-facing Kergui number, in E.164. The wa.me
	// link points at it.
	Number domain.Phone
	// Provider names the messaging provider and its licence notice, for
	// the app's settings screen.
	Provider string
	Notice   string
	Now      Clock
}

// Invitation is what the owner's app shows: a link, the same thing as a QR
// payload, and the notice the provider's licence requires.
type Invitation struct {
	Link      string
	QRPayload string
	ExpiresAt time.Time
	Provider  string
	Notice    string
	// Number is the Kergui number the person writes to.
	Number domain.Phone
}

// Invite issues a code for one member of a household.
func (uc OptIn) Invite(ctx context.Context, tenantID, userID string) (Invitation, error) {
	if uc.Number == "" {
		return Invitation{}, fmt.Errorf("%w: no WhatsApp number is configured", domain.ErrNotActivated)
	}
	code, err := newCode()
	if err != nil {
		return Invitation{}, err
	}
	now := uc.Now()
	hash := HashCode(code)
	if err := uc.Repo.PutPending(ctx, domain.PendingOptIn{
		TenantID: tenantID, UserID: userID, CodeHash: hash[:],
		ExpiresAt: now.Add(CodeTTL).UTC(), CreatedAt: now.UTC(),
	}); err != nil {
		return Invitation{}, err
	}
	// %20 rather than the "+" QueryEscape writes: the link is read by
	// WhatsApp's own client, and a code pre-filled as "KERGUI+AB3D" has
	// been seen to arrive with the plus in it.
	text := strings.ReplaceAll(url.QueryEscape(Keyword+" "+code), "+", "%20")
	link := "https://wa.me/" + strings.TrimPrefix(string(uc.Number), "+") + "?text=" + text
	return Invitation{
		Link: link, QRPayload: link, ExpiresAt: now.Add(CodeTTL).UTC(),
		Provider: uc.Provider, Notice: uc.Notice, Number: uc.Number,
	}, nil
}

// Redeem verifies an inbound "KERGUI <code>" message and records the number.
//
// The number comes from the provider, which authenticated the sender; the
// code proves that this sender is the person the owner's app invited. One
// without the other is not enough, and the code is single-use.
func (uc OptIn) Redeem(ctx context.Context, msg domain.InboundMessage, code string) (domain.OptIn, error) {
	if strings.TrimSpace(code) == "" {
		return domain.OptIn{}, domain.ErrNoOptIn
	}
	hash := HashCode(code)
	pending, err := uc.Repo.ClaimPending(ctx, hash[:], uc.Now().UTC())
	if err != nil {
		return domain.OptIn{}, err
	}
	o := domain.OptIn{
		TenantID: pending.TenantID, UserID: pending.UserID, Phone: msg.From,
		Locale: "fr", CreatedAt: uc.Now().UTC(),
	}
	if err := uc.Repo.SaveOptIn(ctx, o); err != nil {
		return domain.OptIn{}, err
	}
	return o, nil
}

// Stop ends the alerts for a number. It is answered on any "STOP", from any
// number, whether or not it is registered: a person asking not to be
// messaged is never asked to prove anything first.
func (uc OptIn) Stop(ctx context.Context, phone domain.Phone) error {
	return uc.Repo.StopPhone(ctx, phone, uc.Now().UTC())
}

// HashCode is how a code is stored: the hub keeps the hash, so a code read
// out of the database is already worthless.
func HashCode(code string) [32]byte {
	return sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
}

// newCode draws a fresh pairing code.
func newCode() (string, error) {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("alerts: pairing code: %w", err)
	}
	return codeAlphabet.EncodeToString(b), nil
}
