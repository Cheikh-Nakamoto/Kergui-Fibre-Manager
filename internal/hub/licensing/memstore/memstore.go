// Package memstore is an in-memory usecase.Store for the licensing module's
// unit and handler tests. It mirrors the Postgres repository's contract,
// including the unique constraints the use cases rely on, but has no
// rollback: a failed InAccount keeps what it wrote before failing.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

type subKey struct {
	a domain.AccountID
	b domain.BoxID
}

type payKey struct{ provider, ref string }

// Store is the in-memory store. The zero value is not usable; call New.
type Store struct {
	tx sync.Mutex // InAccount: one account write at a time (coarser than Postgres)
	mu sync.Mutex // the maps

	accounts   map[domain.AccountID]usecase.Account
	challenges []usecase.Challenge
	sessions   map[string]usecase.Session
	subs       map[subKey]domain.Subscription
	trials     []usecase.Trial
	transfers  []usecase.Transfer
	payments   map[payKey]usecase.Payment
	// Reminders is the log of claimed reminders, in order.
	Reminders []domain.Subscription
}

var _ usecase.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{
		accounts: map[domain.AccountID]usecase.Account{},
		sessions: map[string]usecase.Session{},
		subs:     map[subKey]domain.Subscription{},
		payments: map[payKey]usecase.Payment{},
	}
}

func (s *Store) InAccount(ctx context.Context, id domain.AccountID, fn func(context.Context) error) error {
	if _, err := s.AccountByID(ctx, id); err != nil {
		return err
	}
	s.tx.Lock()
	defer s.tx.Unlock()
	return fn(ctx)
}

func (s *Store) EnsureAccount(_ context.Context, a usecase.Account) (usecase.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.accounts {
		if got.Phone == a.Phone {
			return got, nil
		}
	}
	s.accounts[a.ID] = a
	return a, nil
}

func (s *Store) AccountByID(_ context.Context, id domain.AccountID) (usecase.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return usecase.Account{}, usecase.ErrNotFound
	}
	return a, nil
}

func (s *Store) AccountByPhone(_ context.Context, p domain.Phone) (usecase.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.Phone == p {
			return a, nil
		}
	}
	return usecase.Account{}, usecase.ErrNotFound
}

func (s *Store) CreateChallenge(_ context.Context, c usecase.Challenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challenges = append(s.challenges, c)
	return nil
}

func (s *Store) ChallengesSince(_ context.Context, p domain.Phone, since time.Time) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, latest := 0, time.Time{}
	for _, c := range s.challenges {
		if c.Phone == p && !c.CreatedAt.Before(since) {
			n++
			if c.CreatedAt.After(latest) {
				latest = c.CreatedAt
			}
		}
	}
	return n, latest, nil
}

func (s *Store) LatestChallenge(_ context.Context, p domain.Phone) (usecase.Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.challenges) - 1; i >= 0; i-- {
		if s.challenges[i].Phone == p {
			return s.challenges[i], nil
		}
	}
	return usecase.Challenge{}, usecase.ErrNotFound
}

func (s *Store) challenge(id string) *usecase.Challenge {
	for i := range s.challenges {
		if s.challenges[i].ID == id {
			return &s.challenges[i]
		}
	}
	return nil
}

func (s *Store) AddAttempt(_ context.Context, id string, max int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.challenge(id)
	if c == nil || c.Attempts >= max {
		return false, nil
	}
	c.Attempts++
	return true, nil
}

func (s *Store) ConsumeChallenge(_ context.Context, id string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.challenge(id)
	if c == nil || c.ConsumedAt != nil {
		return false, nil
	}
	c.ConsumedAt = &at
	return true, nil
}

func (s *Store) CreateSession(_ context.Context, hash []byte, sess usecase.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[string(hash)] = sess
	return nil
}

func (s *Store) SessionByHash(_ context.Context, hash []byte) (usecase.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[string(hash)]
	if !ok {
		return usecase.Session{}, usecase.ErrNotFound
	}
	return sess, nil
}

func (s *Store) RevokeSession(_ context.Context, hash []byte, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[string(hash)]
	if !ok {
		return usecase.ErrNotFound
	}
	if sess.RevokedAt == nil {
		sess.RevokedAt = &at
	}
	s.sessions[string(hash)] = sess
	return nil
}

func (s *Store) Subscription(_ context.Context, a domain.AccountID, b domain.BoxID) (domain.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[subKey{a, b}]
	if !ok {
		return domain.Subscription{}, usecase.ErrNotFound
	}
	return sub, nil
}

func (s *Store) SaveSubscription(_ context.Context, sub domain.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs[subKey{sub.Account, sub.BoxID}] = sub
	return nil
}

func (s *Store) DeleteSubscription(_ context.Context, a domain.AccountID, b domain.BoxID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, subKey{a, b})
	return nil
}

func (s *Store) ListSubscriptions(_ context.Context, a domain.AccountID) ([]domain.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Subscription
	for k, sub := range s.subs {
		if k.a == a {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BoxID < out[j].BoxID })
	return out, nil
}

func (s *Store) TrialUsed(_ context.Context, p domain.Phone, b domain.BoxID) (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var phone, box bool
	for _, t := range s.trials {
		phone = phone || t.Phone == p
		box = box || t.BoxID == b
	}
	return phone, box, nil
}

func (s *Store) RecordTrial(_ context.Context, t usecase.Trial) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.trials {
		if got.Phone == t.Phone || got.BoxID == t.BoxID {
			return usecase.ErrTrialRace
		}
	}
	s.trials = append(s.trials, t)
	return nil
}

func (s *Store) TransfersSince(_ context.Context, a domain.AccountID, since time.Time) ([]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Time
	for _, t := range s.transfers {
		if t.Account == a && !t.At.Before(since) {
			out = append(out, t.At)
		}
	}
	return out, nil
}

func (s *Store) RecordTransfer(_ context.Context, t usecase.Transfer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transfers = append(s.transfers, t)
	return nil
}

// Transfers returns the transfer log.
func (s *Store) Transfers() []usecase.Transfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]usecase.Transfer(nil), s.transfers...)
}

func (s *Store) CreatePayment(_ context.Context, p usecase.Payment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := payKey{p.Provider, p.Reference}
	if _, ok := s.payments[k]; ok {
		return usecase.ErrPaymentMismatch
	}
	s.payments[k] = p
	return nil
}

func (s *Store) PaymentByRef(_ context.Context, provider, ref string) (usecase.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[payKey{provider, ref}]
	if !ok {
		return usecase.Payment{}, usecase.ErrNotFound
	}
	return p, nil
}

func (s *Store) SetPaymentStatus(_ context.Context, id string, st usecase.PaymentStatus, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.payments {
		if p.ID == id {
			p.Status = st
			if st == usecase.PaymentCredited {
				p.CreditedAt = &at
			}
			s.payments[k] = p
			return nil
		}
	}
	return usecase.ErrNotFound
}

func (s *Store) PendingPayments(_ context.Context, provider string, since time.Time, limit int) ([]usecase.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []usecase.Payment
	for _, p := range s.payments {
		if p.Provider == provider && p.Status == usecase.PaymentPending && !p.CreatedAt.Before(since) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) DueReminders(_ context.Context, now, until time.Time, limit int) ([]domain.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Subscription
	for _, sub := range s.subs {
		if sub.ExpiresAt.After(now) && !sub.ExpiresAt.After(until) && !sub.ReminderSentFor.Equal(sub.ExpiresAt) {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ClaimReminder(_ context.Context, sub domain.Subscription, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := subKey{sub.Account, sub.BoxID}
	cur, ok := s.subs[k]
	if !ok || !cur.ExpiresAt.Equal(sub.ExpiresAt) || cur.ReminderSentFor.Equal(cur.ExpiresAt) {
		return false, nil
	}
	s.subs[k] = cur.ReminderSent()
	s.Reminders = append(s.Reminders, cur)
	return true, nil
}

func (s *Store) ReleaseReminder(_ context.Context, sub domain.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := subKey{sub.Account, sub.BoxID}
	cur, ok := s.subs[k]
	if ok && cur.ExpiresAt.Equal(sub.ExpiresAt) {
		cur.ReminderSentFor = sub.ReminderSentFor
		s.subs[k] = cur
	}
	if n := len(s.Reminders); n > 0 {
		s.Reminders = s.Reminders[:n-1]
	}
	return nil
}
