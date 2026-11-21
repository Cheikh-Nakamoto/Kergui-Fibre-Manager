package usecase

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Service is the licensing module's application layer. Its fields are its
// dependencies; the zero values of Now, Rand and Log are filled in.
type Service struct {
	Store  Store
	Sender Sender
	// Gateway is NabooPay (the kergui core's adapter, reused as is). nil
	// turns checkouts off; a reminder then goes out without a link.
	Gateway port.PaymentGateway
	// SigningKey signs the entitlement tokens. It never leaves the hub.
	SigningKey ed25519.PrivateKey
	// ReturnURL is where NabooPay sends the payer back; "?status=success"
	// or "?status=error" is appended.
	ReturnURL string
	Now       Clock
	Rand      io.Reader
	Log       Logger
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

func (s *Service) rand() io.Reader {
	if s.Rand == nil {
		return rand.Reader
	}
	return s.Rand
}

func (s *Service) info(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Info(msg, args...)
	}
}

func (s *Service) warn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
	}
}

// PublicKey is the half of the signing key the app ships with.
func (s *Service) PublicKey() ed25519.PublicKey {
	if len(s.SigningKey) != ed25519.PrivateKeySize {
		return nil
	}
	return s.SigningKey.Public().(ed25519.PublicKey)
}
