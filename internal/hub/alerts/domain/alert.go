package domain

import (
	"context"
	"time"
)

// ReplyWindow is how long a reply can still act on an alert. After that the
// household has to open the app: a "1" sent the next morning must not block
// whatever device happened to be number 1 last night.
const ReplyWindow = time.Hour

// Alert is one message about devices that joined the Wi-Fi, as the hub sent
// it. It exists so a reply can be resolved: "1" means the first device of
// the alert it answers, and nothing else.
type Alert struct {
	ID       string
	TenantID string
	// WindowID is the inventory window this alert announces. It is unique,
	// which is what makes the outbox's at-least-once delivery harmless: the
	// same event twice creates one alert.
	WindowID   string
	Devices    []AlertDevice
	CreatedAt  time.Time
	ReplyUntil time.Time
}

// AlertDevice is one device of an alert, with the number the household
// answers. The number comes from the inventory window, so it means the same
// thing in the message, in the reply and in the audit trail.
type AlertDevice struct {
	No       int    `json:"no"`
	MAC      string `json:"mac"`
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname,omitempty"`
	IP       string `json:"ip,omitempty"`
	SSID     string `json:"ssid,omitempty"`
	// SnapshotSHA256 is the digest of the signed snapshot this device was
	// seen in. The app checks the alert against the relay's signature, so
	// a hub that invented a device has nothing to show.
	SnapshotSHA256 string `json:"snapshot_sha256,omitempty"`
}

// Device finds the device a reply's number refers to.
func (a Alert) Device(no int) (AlertDevice, bool) {
	for _, d := range a.Devices {
		if d.No == no {
			return d, true
		}
	}
	return AlertDevice{}, false
}

// Open reports whether a reply may still act on this alert.
func (a Alert) Open(now time.Time) bool { return now.Before(a.ReplyUntil) }

// Delivery is one alert sent to one person over one channel.
type Delivery struct {
	AlertID  string
	TenantID string
	UserID   string
	Channel  string // "whatsapp" | "push"
	Provider string
	// ProviderMsgID is what the provider called the message, so a reply
	// quoting it resolves to this alert.
	ProviderMsgID string
	SentAt        time.Time
	// Err is why it could not be sent, for the owner to see in the app
	// rather than wonder why nothing arrived.
	Err string
}

// OptIn is a number that agreed to receive a household's alerts. WhatsApp
// requires the person to write first, so this row only ever exists because
// they did (RFC-005: opt-in, the user writes first).
type OptIn struct {
	TenantID  string
	UserID    string
	Phone     Phone
	Locale    string
	CreatedAt time.Time
	StoppedAt *time.Time
}

// Active reports whether this opt-in still receives alerts.
func (o OptIn) Active() bool { return o.StoppedAt == nil }

// PendingOptIn is a code the owner's app showed as a wa.me link or a QR,
// waiting for the person to send it. Only its hash is stored: a code that
// leaks from the database must not be usable.
type PendingOptIn struct {
	TenantID  string
	UserID    string
	CodeHash  []byte
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Repository is the alerts module's storage.
type Repository interface {
	// PutPending records a code the app is showing. One per user: asking
	// again replaces the previous one.
	PutPending(ctx context.Context, p PendingOptIn) error
	// ClaimPending spends a code. It is single-use, so a code seen once by
	// anyone else is already worthless.
	ClaimPending(ctx context.Context, codeHash []byte, now time.Time) (PendingOptIn, error)
	// SaveOptIn records a verified number.
	SaveOptIn(ctx context.Context, o OptIn) error
	// ActiveOptIns lists a household's numbers that still want alerts.
	ActiveOptIns(ctx context.Context, tenantID string) ([]OptIn, error)
	// OptInsByPhone lists the households a number serves. One phone can be
	// the owner of two households, so a reply has to be resolved against
	// the alert, not the number alone.
	OptInsByPhone(ctx context.Context, phone Phone) ([]OptIn, error)
	// StopPhone ends the alerts for a number, everywhere it is registered.
	StopPhone(ctx context.Context, phone Phone, now time.Time) error
	// CreateAlert stores an alert, once per inventory window. It reports
	// whether this call is the one that created it.
	CreateAlert(ctx context.Context, a Alert) (created bool, err error)
	// AlertByWindow returns the alert of an inventory window.
	AlertByWindow(ctx context.Context, tenantID, windowID string) (Alert, error)
	// RecordDelivery stores what was sent, or why it was not.
	RecordDelivery(ctx context.Context, d Delivery) error
	// AlertForReply resolves the alert a reply answers: the one whose
	// message is being quoted, or failing that the newest alert still open
	// that was sent to that number.
	AlertForReply(ctx context.Context, phone Phone, quotedProviderID string, now time.Time) (Alert, error)
}
