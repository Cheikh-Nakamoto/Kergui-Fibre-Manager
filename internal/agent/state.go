package agent

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// ErrNotEnrolled is returned when a relay is asked to run before it has
// joined a household.
var ErrNotEnrolled = errors.New("agent: this relay is not enrolled; run `kergui hub enroll <invite>` first")

// StateData is everything a relay must keep between runs. It holds the
// relay's private identity and its hub token, so it belongs in the vault,
// never in a plain file.
type StateData struct {
	HubURL   string `json:"hubUrl"`
	AgentID  string `json:"agentId"`
	TenantID string `json:"tenantId"`
	Token    string `json:"token"`
	// Identity is the serialised private identity (trust.Identity).
	Identity []byte `json:"identity"`
	// OwnerKey is the owner's signing key, pinned at pairing from the QR
	// fingerprint. Everything the relay accepts is checked against it, so
	// it is the one value a hub must never be able to change.
	OwnerKey ed25519.PublicKey `json:"ownerKey"`
	// Bundle is the newest trust bundle seen, kept so a relay restarting
	// cannot be walked back to an older one by the hub.
	Bundle            trust.Signed[trust.Bundle] `json:"bundle"`
	CredentialVersion uint64                     `json:"credentialVersion"`

	identity *trust.Identity
}

// Identity returns the parsed private identity.
func (s StateData) Keys() *trust.Identity { return s.identity }

// State persists StateData. The CLI keeps it in the AES-GCM vault; the
// mobile app will keep it in the phone's secure store (H3).
type State interface {
	Load() (StateData, error)
	Save(StateData) error
}

// Marshal encodes state for a secret store.
func Marshal(s StateData) ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("agent: encode state: %w", err)
	}
	return b, nil
}

// Unmarshal decodes what Marshal wrote.
func Unmarshal(b []byte) (StateData, error) {
	var s StateData
	if len(b) == 0 {
		return s, ErrNotEnrolled
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return StateData{}, fmt.Errorf("agent: decode state: %w", err)
	}
	return s, nil
}

// MemoryState keeps state in memory. It is what the tests use, and what a
// one-shot relay can use when the household re-pairs every time.
type MemoryState struct{ Data StateData }

// Load implements State.
func (m *MemoryState) Load() (StateData, error) { return m.Data, nil }

// Save implements State.
func (m *MemoryState) Save(s StateData) error { m.Data = s; return nil }
