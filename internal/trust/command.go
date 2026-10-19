package trust

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxCommandTTL caps how long a command stays usable (RFC-005: 60 s). A
// hub that held a command back can therefore only delay it, never bank it.
const MaxCommandTTL = 60 * time.Second

// Command is what the owner's app, a delegated member or the hub's narrow
// WhatsApp key asks a relay to run. The body itself is not signed: its
// digest is, so the hub cannot alter it and the relay never has to trust a
// re-encoding.
type Command struct {
	TenantID   string    `json:"tenant_id"`
	CmdID      string    `json:"cmd_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	BodySHA256 string    `json:"body_sha256"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	KeyID      string    `json:"key_id"`
}

// SignedCommand travels through the hub untouched.
type SignedCommand = Signed[Command]

// Result is the relay's signed answer. Signing it is what stops the hub from
// reporting "blocked ✅" for a command the box refused.
type Result struct {
	TenantID   string          `json:"tenant_id"`
	CmdID      string          `json:"cmd_id"`
	Epoch      int64           `json:"epoch"`
	Status     int             `json:"status"`
	Body       json.RawMessage `json:"body,omitempty"`
	ExecutedAt time.Time       `json:"executed_at"`
	KeyID      string          `json:"key_id"`
}

// SignedResult travels back through the hub untouched.
type SignedResult = Signed[Result]

// Snapshot is state the relay pushes on its own: the device list today,
// traffic counters and SSIDs from H2 on. Signed for the same reason as a
// result — the hub must not be able to invent a device list.
type Snapshot struct {
	TenantID string          `json:"tenant_id"`
	Kind     string          `json:"kind"` // "devices"
	AgentID  string          `json:"agent_id"`
	Epoch    int64           `json:"epoch"`
	AsOf     time.Time       `json:"as_of"`
	Body     json.RawMessage `json:"body"`
	KeyID    string          `json:"key_id"`
}

// SignedSnapshot is stored by the hub and served to the app as-is.
type SignedSnapshot = Signed[Snapshot]

// NewCommand signs a command for a relay to run. ttl is capped at
// MaxCommandTTL; a caller asking for more is refused rather than trimmed, so
// a mistaken caller hears about it.
func NewCommand(id *Identity, tenantID, method, path string, body []byte, now time.Time, ttl time.Duration) (SignedCommand, error) {
	if ttl <= 0 || ttl > MaxCommandTTL {
		return SignedCommand{}, fmt.Errorf("%w: ttl %s", ErrTooLongLived, ttl)
	}
	cmdID, err := newCmdID(rand.Reader)
	if err != nil {
		return SignedCommand{}, err
	}
	return Sign(id, DomainCommand, Command{
		TenantID:   tenantID,
		CmdID:      cmdID,
		Method:     strings.ToUpper(method),
		Path:       path,
		BodySHA256: BodyDigest(body),
		IssuedAt:   now.UTC(),
		ExpiresAt:  now.Add(ttl).UTC(),
		KeyID:      id.KeyID(),
	})
}

// NewResult signs the outcome of a command.
func NewResult(id *Identity, tenantID, cmdID string, epoch int64, status int, body []byte, now time.Time) (SignedResult, error) {
	return Sign(id, DomainResult, Result{
		TenantID:   tenantID,
		CmdID:      cmdID,
		Epoch:      epoch,
		Status:     status,
		Body:       rawOrNull(body),
		ExecutedAt: now.UTC(),
		KeyID:      id.KeyID(),
	})
}

// NewSnapshot signs a piece of state the relay observed.
func NewSnapshot(id *Identity, tenantID, kind, agentID string, epoch int64, body []byte, now time.Time) (SignedSnapshot, error) {
	return Sign(id, DomainSnapshot, Snapshot{
		TenantID: tenantID,
		Kind:     kind,
		AgentID:  agentID,
		Epoch:    epoch,
		AsOf:     now.UTC(),
		Body:     rawOrNull(body),
		KeyID:    id.KeyID(),
	})
}

// BodyDigest is the hex SHA-256 a command commits to. An empty body hashes
// like an empty string, so "no body" and "empty body" cannot be swapped.
func BodyDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// BlockTargetMAC returns the MAC a block/unblock path acts on, and whether
// the path is one of those. It is used to keep a narrow delegation off a
// protected device.
func BlockTargetMAC(path string) (string, bool) {
	p := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/")
	parts := strings.Split(p, "/")
	// api/devices/{mac}/{action}
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "devices" {
		return "", false
	}
	switch parts[3] {
	case "block", "unblock", "wifi-block", "wifi-unblock":
		return parts[2], true
	}
	return "", false
}

func newCmdID(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("trust: command id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// rawOrNull keeps a nil body out of the JSON rather than writing invalid
// RawMessage bytes.
func rawOrNull(body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	if json.Valid(body) {
		return json.RawMessage(body)
	}
	// A non-JSON body (an HTML error page from the box) still has to travel
	// signed, so it goes as a JSON string.
	quoted, err := json.Marshal(string(body))
	if err != nil {
		return nil
	}
	return quoted
}

// isRead reports whether a method only reads, which is all a view
// delegation may do.
func isRead(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}
