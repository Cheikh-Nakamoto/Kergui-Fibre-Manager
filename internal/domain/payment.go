package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// PaymentStatus is our reading of a provider transaction.
type PaymentStatus string

const (
	PaymentPending   PaymentStatus = "pending"
	PaymentPaid      PaymentStatus = "paid"
	PaymentCancelled PaymentStatus = "cancelled"
	PaymentFailed    PaymentStatus = "failed"
	PaymentRefunded  PaymentStatus = "refunded"
)

// PaymentReminderSnooze is how long "pay later" hides the payment page.
const PaymentReminderSnooze = 24 * time.Hour

// PaymentKind tells a monthly invoice from an on-demand (test) payment.
type PaymentKind string

const (
	PaymentTest    PaymentKind = "test"    // one device, requested by the admin
	PaymentInvoice PaymentKind = "invoice" // monthly, one member, all their devices
)

// PaymentItem is the share of one device in a payment.
type PaymentItem struct {
	MAC   MAC    `json:"mac"`
	Bytes uint64 `json:"bytes"`
}

// Payment is a request to pay for consumption over a period.
type Payment struct {
	ID           string        `json:"id"`
	RouterID     string        `json:"router_id"`
	Kind         PaymentKind   `json:"kind"`
	MemberID     string        `json:"member_id,omitempty"`
	MAC          MAC           `json:"mac"` // test payments only
	Items        []PaymentItem `json:"items"`
	Period       string        `json:"period"`
	Bytes        uint64        `json:"bytes"`
	AmountXOF    int64         `json:"amount_xof"`
	CustomerName string        `json:"customer_name"`
	Phone        string        `json:"phone"`
	OrderID      string        `json:"order_id"`
	CheckoutURL  string        `json:"checkout_url"`
	Status       PaymentStatus `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	PaidAt       time.Time     `json:"paid_at,omitempty"`
	DueAt        time.Time     `json:"due_at,omitempty"` // invoices only
}

// Overdue reports whether an unpaid invoice is past its due date plus grace.
func (p Payment) Overdue(now time.Time, grace time.Duration) bool {
	return p.Kind == PaymentInvoice && p.Status == PaymentPending && !p.DueAt.IsZero() && now.After(p.DueAt.Add(grace))
}

// BillingSettings set how consumption turns into an amount, and when unpaid
// invoices lead to a cut.
type BillingSettings struct {
	PricePerGB int64 `json:"price_per_gb"` // XOF per 10^9 bytes
	MinAmount  int64 `json:"min_amount"`   // smallest amount ever requested, XOF
	DueDays    int64 `json:"due_days"`     // days after the monthly close to pay
	GraceDays  int64 `json:"grace_days"`   // extra days before the cut
	// EnforceCuts actually cuts internet for overdue members. While false the
	// enforcement only logs what it would do (simulation).
	EnforceCuts bool `json:"enforce_cuts"`
	// CapGB cuts a device whose unpaid consumption this month reaches it,
	// until its payment link is paid. 0 disables the ceiling.
	CapGB int64 `json:"cap_gb"`
}

// CapBytes is the ceiling in bytes (0 = none).
func (s BillingSettings) CapBytes() uint64 {
	if s.CapGB <= 0 {
		return 0
	}
	return uint64(s.CapGB) * 1_000_000_000
}

// DefaultBillingSettings are used until the admin sets their own.
var DefaultBillingSettings = BillingSettings{PricePerGB: 500, MinAmount: 100, DueDays: 5, GraceDays: 3, CapGB: 100}

// Grace is GraceDays as a duration.
func (s BillingSettings) Grace() time.Duration { return time.Duration(s.GraceDays) * 24 * time.Hour }

// Amount prices bytes, rounded up to the franc and raised to MinAmount. Zero
// bytes cost nothing.
func (s BillingSettings) Amount(bytes uint64) int64 {
	if bytes == 0 || s.PricePerGB <= 0 {
		return 0
	}
	const gb = 1_000_000_000
	// ceil(bytes * price / gb) without overflow for any realistic volume.
	whole := int64(bytes/gb) * s.PricePerGB
	rest := (int64(bytes%gb)*s.PricePerGB + gb - 1) / gb
	amount := whole + rest
	if amount < s.MinAmount {
		amount = s.MinAmount
	}
	return amount
}

var senegalMobile = regexp.MustCompile(`^7[05678]\d{7}$`)

// NormalizeSenegalPhone accepts 771234567, 221771234567 or +221 77 123 45 67
// and returns +221771234567.
func NormalizeSenegalPhone(s string) (string, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	digits = strings.TrimPrefix(digits, "00221")
	if len(digits) == 12 {
		digits = strings.TrimPrefix(digits, "221")
	}
	if !senegalMobile.MatchString(digits) {
		return "", fmt.Errorf("%w: numéro sénégalais attendu (ex. 77 123 45 67)", ErrInvalidInput)
	}
	return "+221" + digits, nil
}

// PreviousPeriod is the month before period ("2026-10" -> "2026-09").
func PreviousPeriod(period string) (string, error) {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return "", fmt.Errorf("%w: période attendue au format AAAA-MM", ErrInvalidInput)
	}
	return t.AddDate(0, -1, 0).Format("2006-01"), nil
}
