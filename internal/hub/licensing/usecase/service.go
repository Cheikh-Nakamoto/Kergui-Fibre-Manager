package usecase

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"slices"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Service is the licensing module's application layer. Its fields are its
// dependencies; the zero values of Now, Rand and Log are filled in.
type Service struct {
	Store  Store
	Sender Sender
	// Gateway is NabooPay (the kergui core's adapter, reused as is). nil
	// turns checkouts off.
	Gateway port.PaymentGateway
	// SigningKey signs the entitlement tokens. It never leaves the hub.
	// It is the active key of a rotation (docs/licensing-key-rotation.md).
	SigningKey ed25519.PrivateKey
	// NextKeys and RetiringKeys are published beside the active key: the
	// next one before it signs (app updates embed it), the retiring ones
	// once they no longer sign, until their tokens have run out.
	// RevokedKeyIDs are published so the apps stop honouring their tokens.
	NextKeys      []ed25519.PublicKey
	RetiringKeys  []ed25519.PublicKey
	RevokedKeyIDs []string
	// ReturnURL is where NabooPay sends the payer back; "?status=success"
	// or "?status=error" is appended.
	ReturnURL string
	// BuyURL is the hub's public purchase page (GET /licensing/v1/buy),
	// which the J-3 expiry reminder links to. Empty sends it without a link.
	BuyURL string
	// Play is the Google Play Developer API; nil turns the Play channel
	// off. PlayPackage is the app's package name: notifications for any
	// other are ignored.
	Play        PlayVerifier
	PlayPackage string
	Now         Clock
	Rand        io.Reader
	Log         Logger
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

// KeyList is the hub's published keys, active first, and the revoked key
// ids, signed by the active key (entitlement.SignKeyList): an app that
// trusts any key of the list's previous versions learns the others from it.
func (s *Service) KeyList() (entitlement.KeyList, string, error) {
	pub := s.PublicKey()
	if pub == nil {
		return entitlement.KeyList{}, "", entitlement.ErrInvalidKey
	}
	l := entitlement.KeyList{IssuedAt: s.now(), Revoked: slices.Clone(s.RevokedKeyIDs)}
	add := func(st entitlement.KeyStatus, keys ...ed25519.PublicKey) {
		for _, k := range keys {
			l.Keys = append(l.Keys, entitlement.PublishedKey{KeyID: entitlement.KeyID(k), PublicKey: k, Status: st})
		}
	}
	add(entitlement.KeyActive, pub)
	add(entitlement.KeyNext, s.NextKeys...)
	add(entitlement.KeyRetiring, s.RetiringKeys...)
	doc, err := entitlement.SignKeyList(s.SigningKey, l)
	if err != nil {
		return entitlement.KeyList{}, "", err
	}
	l.KeyID = entitlement.KeyID(pub)
	l.IssuedAt = l.IssuedAt.Truncate(time.Second)
	return l, doc, nil
}

// PublicKey is the half of the active signing key, the one the tokens
// issued now verify with.
func (s *Service) PublicKey() ed25519.PublicKey {
	if len(s.SigningKey) != ed25519.PrivateKeySize {
		return nil
	}
	return s.SigningKey.Public().(ed25519.PublicKey)
}
