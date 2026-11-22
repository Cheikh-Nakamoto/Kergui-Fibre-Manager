package usecase

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"crypto/rand"

	"github.com/google/uuid"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// Sign-in parameters. The RFC only says "a one-time code over WhatsApp";
// the numbers are the usual ones for a 6-digit code.
const (
	// OTPTTL is how long a code stays usable.
	OTPTTL = 10 * time.Minute
	// OTPMaxAttempts is how many guesses one code allows: 5 in a million.
	OTPMaxAttempts = 5
	// OTPResendAfter is the shortest gap between two codes to a number.
	OTPResendAfter = time.Minute
	// OTPPerHour caps the codes one number receives in an hour, so the
	// route cannot be used to spam someone's WhatsApp.
	OTPPerHour = 5
	// SessionTTL is how long a signed-in phone stays signed in. The token
	// it refreshes with lasts far longer than the entitlement it fetches,
	// which is what keeps the household from re-typing codes.
	SessionTTL = 365 * 24 * time.Hour
	// SessionTokenPrefix marks a session token in logs and support
	// messages, as agents' kga_ does.
	SessionTokenPrefix = "kgs_"
	sessionTokenBytes  = 32
	otpDigits          = 6
	maxDeviceLen       = 64
)

// RateLimitError says how long to wait before asking for another code.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%v (retry in %s)", ErrOTPRateLimited, e.RetryAfter.Round(time.Second))
}

func (e *RateLimitError) Unwrap() error { return ErrOTPRateLimited }

// OTPSent is what the app shows after asking for a code.
type OTPSent struct {
	Phone     domain.Phone
	ExpiresAt time.Time
	// ResendAt is the earliest time another code may be asked for.
	ResendAt time.Time
}

// RequestOTP sends a 6-digit code to a number over WhatsApp. Only the hash
// of the code is stored.
func (s *Service) RequestOTP(ctx context.Context, rawPhone string) (OTPSent, error) {
	p, err := domain.ParsePhone(rawPhone)
	if err != nil {
		return OTPSent{}, err
	}
	now := s.now()
	n, latest, err := s.Store.ChallengesSince(ctx, p, now.Add(-time.Hour))
	if err != nil {
		return OTPSent{}, err
	}
	if n > 0 {
		if wait := latest.Add(OTPResendAfter).Sub(now); wait > 0 {
			return OTPSent{}, &RateLimitError{RetryAfter: wait}
		}
	}
	if n >= OTPPerHour {
		// The oldest code of the hour is not known here; an hour after the
		// newest is a safe upper bound to tell the person.
		return OTPSent{}, &RateLimitError{RetryAfter: latest.Add(time.Hour).Sub(now)}
	}
	code, err := newOTP(s.rand())
	if err != nil {
		return OTPSent{}, err
	}
	c := Challenge{
		ID: uuid.NewString(), Phone: p, ExpiresAt: now.Add(OTPTTL), CreatedAt: now,
	}
	c.CodeHash = hashOTP(c.ID, code)
	if err := s.Store.CreateChallenge(ctx, c); err != nil {
		return OTPSent{}, err
	}
	text := fmt.Sprintf("Kergui : votre code de connexion est %s. Il expire dans %d minutes. "+
		"Ne le communiquez à personne, Kergui ne vous le demandera jamais.", code, int(OTPTTL/time.Minute))
	if _, err := s.Sender.Send(ctx, p, alerts.OutboundMessage{Text: text}); err != nil {
		return OTPSent{}, fmt.Errorf("licensing: send the code: %w", err)
	}
	s.info("licensing: sign-in code sent", "to", p.Masked())
	return OTPSent{Phone: p, ExpiresAt: c.ExpiresAt, ResendAt: now.Add(OTPResendAfter)}, nil
}

// SignIn is a verified phone: its account and its session token, which
// appears here once and is stored only as a hash.
type SignIn struct {
	Account   Account
	Token     string
	ExpiresAt time.Time
	// Hub is the same sign-in on the kernel: the household and its token
	// pair. nil when the service has no Identity.
	Hub *HubSession
}

// VerifyOTP checks a code and signs the phone in, creating the account the
// first time its number is verified. Every phone of the household signs in
// with the account's number and gets a session of its own.
func (s *Service) VerifyOTP(ctx context.Context, rawPhone, code, device string) (SignIn, error) {
	p, err := domain.ParsePhone(rawPhone)
	if err != nil {
		return SignIn{}, err
	}
	now := s.now()
	c, err := s.Store.LatestChallenge(ctx, p)
	if errors.Is(err, ErrNotFound) {
		return SignIn{}, ErrOTPExpired
	}
	if err != nil {
		return SignIn{}, err
	}
	if c.ConsumedAt != nil || !now.Before(c.ExpiresAt) {
		return SignIn{}, ErrOTPExpired
	}
	if c.Attempts >= OTPMaxAttempts {
		return SignIn{}, ErrOTPLocked
	}
	// The attempt is counted before the comparison, so a burst of guesses
	// racing each other cannot get more than OTPMaxAttempts.
	ok, err := s.Store.AddAttempt(ctx, c.ID, OTPMaxAttempts)
	if err != nil {
		return SignIn{}, err
	}
	if !ok {
		return SignIn{}, ErrOTPLocked
	}
	if subtle.ConstantTimeCompare(hashOTP(c.ID, strings.TrimSpace(code)), c.CodeHash) != 1 {
		if c.Attempts+1 >= OTPMaxAttempts {
			// That was the last guess: say so now rather than on the next.
			return SignIn{}, ErrOTPLocked
		}
		return SignIn{}, ErrOTPInvalid
	}
	if ok, err := s.Store.ConsumeChallenge(ctx, c.ID, now); err != nil {
		return SignIn{}, err
	} else if !ok {
		// Another request with the same code won the race.
		return SignIn{}, ErrOTPExpired
	}
	acc, err := s.Store.EnsureAccount(ctx, Account{ID: domain.AccountID(uuid.NewString()), Phone: p, CreatedAt: now})
	if err != nil {
		return SignIn{}, err
	}
	// The household first: a phone is signed in to both or to neither, so
	// a failure here leaves no licensing session without its household.
	var hub *HubSession
	if s.Identity != nil {
		h, err := s.Identity.SignIn(ctx, acc)
		if err != nil {
			return SignIn{}, fmt.Errorf("licensing: hub sign-in: %w", err)
		}
		hub = &h
	}
	token, hash, err := newSessionToken(s.rand())
	if err != nil {
		return SignIn{}, err
	}
	sess := Session{Account: acc.ID, Device: clip(device, maxDeviceLen), CreatedAt: now, ExpiresAt: now.Add(SessionTTL)}
	if err := s.Store.CreateSession(ctx, hash, sess); err != nil {
		return SignIn{}, err
	}
	s.info("licensing: phone signed in", "account", acc.ID, "phone", p.Masked())
	return SignIn{Account: acc, Token: token, ExpiresAt: sess.ExpiresAt, Hub: hub}, nil
}

// Authenticate resolves a session token to its account.
func (s *Service) Authenticate(ctx context.Context, token string) (Account, error) {
	if !strings.HasPrefix(token, SessionTokenPrefix) {
		return Account{}, ErrUnauthenticated
	}
	sess, err := s.Store.SessionByHash(ctx, HashToken(token))
	if errors.Is(err, ErrNotFound) {
		return Account{}, ErrUnauthenticated
	}
	if err != nil {
		return Account{}, err
	}
	if sess.RevokedAt != nil || !s.now().Before(sess.ExpiresAt) {
		return Account{}, ErrUnauthenticated
	}
	acc, err := s.Store.AccountByID(ctx, sess.Account)
	if errors.Is(err, ErrNotFound) {
		return Account{}, ErrUnauthenticated
	}
	return acc, err
}

// SignOut revokes one phone's session; the others stay signed in.
func (s *Service) SignOut(ctx context.Context, token string) error {
	err := s.Store.RevokeSession(ctx, HashToken(token), s.now())
	if errors.Is(err, ErrNotFound) {
		return ErrUnauthenticated
	}
	return err
}

// newOTP draws a uniform 6-digit code.
func newOTP(r io.Reader) (string, error) {
	n, err := rand.Int(r, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("licensing: code: %w", err)
	}
	return fmt.Sprintf("%0*d", otpDigits, n.Int64()), nil
}

// hashOTP salts the code with its challenge id. A 6-digit code cannot be
// protected by a hash against someone holding the table; the hash keeps it
// out of backups and logs, and the code dies in ten minutes anyway.
func hashOTP(id, code string) []byte {
	sum := sha256.Sum256([]byte(id + ":" + code))
	return sum[:]
}

func newSessionToken(r io.Reader) (token string, hash []byte, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", nil, fmt.Errorf("licensing: session token: %w", err)
	}
	token = SessionTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken is how session tokens are stored: they are 256-bit random
// values, so SHA-256 without stretching is enough.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Do not cut a UTF-8 sequence in half.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
