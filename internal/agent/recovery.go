package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Recovery of a lost owner phone, the relay's side (issue #48, RFC-005
// "Lost owner phone"). At home, on the Wi-Fi, the new phone asks the relay
// for a challenge, proves it knows the box password (trust.RecoveryProof:
// argon2id-stretched, bound to the household, the nonce and the new key) and
// gets back a handover the relay signs: "the household's owner key is now
// this one". The new phone signs the next bundle with it; every relay and
// member holding the current bundle checks the handover against it.
//
// This exchange is never the hub's to carry. It is served by its own
// listener on the home network (RecoveryPort), not by the handler the hub's
// commands run against — /recovery is on no allow-list — and it answers
// only callers with a LAN address. A nonce is single use and short-lived, so
// a captured exchange cannot be replayed, and the proof covers the new key,
// so a relayed one cannot be redirected to another key. Each claim costs an
// argon2id derivation, so claims are few: a household recovers once, a
// password guesser is slowed to a crawl.

// RecoveryPort is where a relay listens for recovery on the home network.
const RecoveryPort = 8791

// Recovery limits.
const (
	// RecoveryNonceTTL is how long a challenge may be answered.
	RecoveryNonceTTL = 2 * time.Minute
	// RecoveryMaxNonces bounds the challenges outstanding at once.
	RecoveryMaxNonces = 4
	// RecoveryMaxClaims and RecoveryClaimWindow bound the claims, right or
	// wrong: each one is an argon2id derivation on the relay.
	RecoveryMaxClaims   = 5
	RecoveryClaimWindow = 10 * time.Minute
)

// Recovery refusals, as the claimant sees them.
var (
	ErrRecoveryNotLAN       = errors.New("agent: recovery is only answered on the home network")
	ErrRecoveryNotReady     = errors.New("agent: this relay is not signed into the household yet")
	ErrRecoveryNoPassword   = errors.New("agent: this relay holds no box password yet")
	ErrRecoveryNonce        = errors.New("agent: unknown, used or expired recovery challenge")
	ErrRecoveryProof        = errors.New("agent: the proof does not match the box password")
	ErrRecoveryRateLimited  = errors.New("agent: too many recovery attempts, wait a few minutes")
	ErrRecoveryTooManyNonce = errors.New("agent: too many recovery challenges outstanding")
)

// BoxPassword reads the box password the relay holds ("" when none).
type BoxPassword func(ctx context.Context) (string, error)

// RecoveryConfig wires the relay's recovery listener.
type RecoveryConfig struct {
	State    State
	Password BoxPassword
	Clock    func() time.Time
	Rand     io.Reader
	Log      port.Logger
}

// Recovery is the relay's recovery handler.
type Recovery struct {
	cfg RecoveryConfig

	mu     sync.Mutex
	nonces map[string]time.Time
	claims []time.Time
	// work serialises the argon2id derivations: one at a time, whatever
	// the network sends.
	work sync.Mutex
}

// NewRecovery builds the handler.
func NewRecovery(cfg RecoveryConfig) *Recovery {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Log == nil {
		cfg.Log = nopLogger{}
	}
	return &Recovery{cfg: cfg, nonces: map[string]time.Time{}}
}

// Wire shapes.
type (
	signedWire struct {
		Payload []byte `json:"payload"`
		Sig     []byte `json:"sig"`
	}
	// RecoveryChallenge is the relay's answer to POST /recovery/v1/challenge.
	RecoveryChallenge struct {
		TenantID  string     `json:"tenantId"`
		AgentID   string     `json:"agentId"`
		Nonce     []byte     `json:"nonce"`
		ExpiresAt time.Time  `json:"expiresAt"`
		Bundle    signedWire `json:"bundle"`
	}
	// RecoveryClaim is the new phone's POST /recovery/v1/claim.
	RecoveryClaim struct {
		Nonce   []byte `json:"nonce"`
		Ed25519 []byte `json:"ed25519"`
		X25519  []byte `json:"x25519"`
		Proof   []byte `json:"proof"`
	}
	// RecoveryGrant is the relay's answer to a claim that proved the box
	// password: the handover it signed and the bundle it holds.
	RecoveryGrant struct {
		Handover          signedWire `json:"handover"`
		Bundle            signedWire `json:"bundle"`
		CredentialVersion uint64     `json:"credentialVersion"`
	}
)

// ServeHTTP answers POST /recovery/v1/challenge and POST /recovery/v1/claim.
func (r *Recovery) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !onLAN(req.RemoteAddr) {
		r.cfg.Log.Warnf("recovery: refused a caller from %s (not the home network)", req.RemoteAddr)
		recoveryError(w, http.StatusForbidden, "not_lan", ErrRecoveryNotLAN)
		return
	}
	if req.Method != http.MethodPost {
		recoveryError(w, http.StatusMethodNotAllowed, "method", errors.New("POST only"))
		return
	}
	switch req.URL.Path {
	case "/recovery/v1/challenge":
		out, err := r.Challenge(req.Context())
		if err != nil {
			r.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case "/recovery/v1/claim":
		var c RecoveryClaim
		if err := json.NewDecoder(io.LimitReader(req.Body, 16<<10)).Decode(&c); err != nil {
			recoveryError(w, http.StatusBadRequest, "malformed", err)
			return
		}
		out, err := r.Claim(req.Context(), c)
		if err != nil {
			r.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	default:
		http.NotFound(w, req)
	}
}

func (r *Recovery) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRecoveryNotReady), errors.Is(err, ErrNotEnrolled):
		recoveryError(w, http.StatusConflict, "not_ready", err)
	case errors.Is(err, ErrRecoveryNoPassword):
		recoveryError(w, http.StatusConflict, "no_password", err)
	case errors.Is(err, ErrRecoveryNonce):
		recoveryError(w, http.StatusGone, "nonce", err)
	case errors.Is(err, ErrRecoveryProof):
		recoveryError(w, http.StatusForbidden, "proof", err)
	case errors.Is(err, ErrRecoveryRateLimited), errors.Is(err, ErrRecoveryTooManyNonce):
		recoveryError(w, http.StatusTooManyRequests, "rate_limited", err)
	case errors.Is(err, trust.ErrMalformed):
		recoveryError(w, http.StatusBadRequest, "malformed", err)
	default:
		recoveryError(w, http.StatusInternalServerError, "internal", errors.New("recovery failed"))
		r.cfg.Log.Warnf("recovery: %v", err)
	}
}

// held is the relay's state and the bundle it holds, checked again against
// the owner key it pinned: a stored bundle is believed only on its signature.
func (r *Recovery) held() (StateData, *trust.Identity, trust.Bundle, error) {
	st, err := r.cfg.State.Load()
	if err != nil {
		return StateData{}, nil, trust.Bundle{}, err
	}
	if st.TenantID == "" || len(st.OwnerKey) == 0 || len(st.Bundle.Payload) == 0 {
		return StateData{}, nil, trust.Bundle{}, ErrRecoveryNotReady
	}
	id, err := trust.ParseIdentity(st.Identity)
	if err != nil {
		return StateData{}, nil, trust.Bundle{}, err
	}
	b, err := st.Bundle.Open(trust.DomainBundle, st.OwnerKey)
	if err != nil || b.TenantID != st.TenantID {
		return StateData{}, nil, trust.Bundle{}, fmt.Errorf("%w: the stored bundle", ErrRecoveryNotReady)
	}
	if r, ok := b.Relay(st.AgentID); !ok || r.KeyID() != id.KeyID() {
		return StateData{}, nil, trust.Bundle{}, ErrRecoveryNotReady
	}
	return st, id, b, nil
}

// Challenge hands out a fresh nonce and the bundle the relay holds.
func (r *Recovery) Challenge(ctx context.Context) (RecoveryChallenge, error) {
	st, _, _, err := r.held()
	if err != nil {
		return RecoveryChallenge{}, err
	}
	now := r.cfg.Clock()
	r.mu.Lock()
	r.purgeLocked(now)
	if len(r.nonces) >= RecoveryMaxNonces {
		r.mu.Unlock()
		return RecoveryChallenge{}, ErrRecoveryTooManyNonce
	}
	nonce := make([]byte, trust.RecoveryNonceSize)
	if _, err := io.ReadFull(r.cfg.Rand, nonce); err != nil {
		r.mu.Unlock()
		return RecoveryChallenge{}, err
	}
	exp := now.Add(RecoveryNonceTTL)
	r.nonces[string(nonce)] = exp
	r.mu.Unlock()
	r.cfg.Log.Infof("recovery: challenge handed out on the home network")
	return RecoveryChallenge{
		TenantID: st.TenantID, AgentID: st.AgentID, Nonce: nonce, ExpiresAt: exp.UTC(),
		Bundle: signedWire{Payload: st.Bundle.Payload, Sig: st.Bundle.Sig},
	}, nil
}

// Claim checks a proof and, when it holds, signs the handover to the new key.
// The nonce is spent whatever the outcome.
func (r *Recovery) Claim(ctx context.Context, c RecoveryClaim) (RecoveryGrant, error) {
	newKeys := trust.PublicKeys{Ed25519: c.Ed25519, X25519: c.X25519}
	if newKeys.KeyID() == "" || len(c.X25519) != 32 {
		return RecoveryGrant{}, fmt.Errorf("%w: new owner keys", trust.ErrMalformed)
	}
	now := r.cfg.Clock()
	r.mu.Lock()
	r.purgeLocked(now)
	exp, ok := r.nonces[string(c.Nonce)]
	delete(r.nonces, string(c.Nonce))
	if !ok || !now.Before(exp) {
		r.mu.Unlock()
		return RecoveryGrant{}, ErrRecoveryNonce
	}
	if len(r.claims) >= RecoveryMaxClaims {
		r.mu.Unlock()
		return RecoveryGrant{}, ErrRecoveryRateLimited
	}
	r.claims = append(r.claims, now)
	r.mu.Unlock()

	st, id, b, err := r.held()
	if err != nil {
		return RecoveryGrant{}, err
	}
	password, err := r.cfg.Password(ctx)
	if err != nil {
		return RecoveryGrant{}, err
	}
	if password == "" {
		return RecoveryGrant{}, ErrRecoveryNoPassword
	}
	r.work.Lock()
	ok = trust.VerifyRecovery(c.Proof, st.TenantID, password, c.Nonce, ed25519.PublicKey(c.Ed25519))
	r.work.Unlock()
	if !ok {
		r.cfg.Log.Warnf("recovery: a proof did not match the box password")
		return RecoveryGrant{}, ErrRecoveryProof
	}
	ho, err := trust.NewHandover(id, b, newKeys, trust.ViaRelay, c.Nonce, now)
	if err != nil {
		return RecoveryGrant{}, err
	}
	r.cfg.Log.Infof("recovery: the box password was proved on the home network; household handed to key %s", newKeys.KeyID())
	return RecoveryGrant{
		Handover:          signedWire{Payload: ho.Payload, Sig: ho.Sig},
		Bundle:            signedWire{Payload: st.Bundle.Payload, Sig: st.Bundle.Sig},
		CredentialVersion: st.CredentialVersion,
	}, nil
}

func (r *Recovery) purgeLocked(now time.Time) {
	for n, exp := range r.nonces {
		if !now.Before(exp) {
			delete(r.nonces, n)
		}
	}
	kept := r.claims[:0]
	for _, t := range r.claims {
		if now.Sub(t) < RecoveryClaimWindow {
			kept = append(kept, t)
		}
	}
	r.claims = kept
}

// onLAN reports whether a caller's address is on a home network: private,
// link-local or this machine. A hub is none of these to a relay.
func onLAN(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func recoveryError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, map[string]string{"code": code, "error": err.Error()})
}

// ServeRecovery listens for recovery on addr until ctx ends. addr is the
// relay's address on the home network with RecoveryPort; a relay that does
// not know it does not listen (it would otherwise answer on every
// interface).
func ServeRecovery(ctx context.Context, addr string, h http.Handler, log port.Logger) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if log != nil {
		log.Infof("recovery: listening on the home network at %s", addr)
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// RecoveryAddr is host:RecoveryPort for a LAN address.
func RecoveryAddr(lanIP string) string {
	return net.JoinHostPort(lanIP, fmt.Sprint(RecoveryPort))
}
