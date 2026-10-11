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

// Payment is a request to pay for a device's consumption over a period.
type Payment struct {
	ID           string        `json:"id"`
	RouterID     string        `json:"router_id"`
	MAC          MAC           `json:"mac"`
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
}

// BillingSettings set how consumption turns into an amount.
type BillingSettings struct {
	PricePerGB int64 `json:"price_per_gb"` // XOF per 10^9 bytes
	MinAmount  int64 `json:"min_amount"`   // smallest amount ever requested, XOF
}

// DefaultBillingSettings are used until the admin sets their own.
var DefaultBillingSettings = BillingSettings{PricePerGB: 500, MinAmount: 100}

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
