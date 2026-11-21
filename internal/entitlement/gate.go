package entitlement

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotEntitled is the class of every refusal of a paid action: match it
// with errors.Is. The concrete error is a *NotEntitledError.
var ErrNotEntitled = errors.New("not entitled")

// Reason says why a paid action was refused. It is for logs and tests; the
// app shows one neutral sentence whatever the reason (RFC-006).
type Reason string

const (
	ReasonNoToken    Reason = "no_token"    // no token held
	ReasonNoKey      Reason = "no_key"      // the build trusts no key
	ReasonMalformed  Reason = "malformed"   // the token does not parse
	ReasonUnknownKey Reason = "unknown_key" // signed by a key not trusted
	ReasonSignature  Reason = "bad_signature"
	ReasonExpired    Reason = "expired"   // past expiry and grace
	ReasonNoBox      Reason = "no_box"    // the box serial is unknown
	ReasonOtherBox   Reason = "other_box" // the token is for another box
	ReasonTier       Reason = "tier"      // the token's tier is too low
	ReasonUnknown    Reason = "unknown_feature"
)

// NotEntitledError refuses one paid action.
type NotEntitledError struct {
	Feature Feature
	// Required is the lowest tier that unlocks Feature ("" for an unknown
	// feature).
	Required Tier
	Reason   Reason
}

func (e *NotEntitledError) Error() string {
	return fmt.Sprintf("not entitled: %s needs %s (%s)", e.Feature, e.Required, e.Reason)
}

// Is makes errors.Is(err, ErrNotEntitled) true.
func (e *NotEntitledError) Is(target error) bool { return target == ErrNotEntitled }

// Authorize decides whether feature f may run on the box whose serial is box,
// at now, with the token held (empty: none). A free feature always passes,
// whatever the token, the keys or the box: giving access back never depends
// on a pass. A paid one needs a token that verifies with keys, is for this
// box, has not run past its expiry plus OfflineGrace, and carries a tier
// that unlocks f. Every refusal is a *NotEntitledError.
func Authorize(keys KeySet, token, box string, f Feature, now time.Time) error {
	min, known := MinTier(f)
	if known && min == TierFree {
		return nil
	}
	deny := func(r Reason) error { return &NotEntitledError{Feature: f, Required: min, Reason: r} }
	if !known {
		return deny(ReasonUnknown)
	}
	if token == "" {
		return deny(ReasonNoToken)
	}
	if keys.Len() == 0 {
		return deny(ReasonNoKey)
	}
	c, _, err := Verify(keys, token, now)
	switch {
	case errors.Is(err, ErrUnknownKey):
		return deny(ReasonUnknownKey)
	case errors.Is(err, ErrBadSignature):
		return deny(ReasonSignature)
	case errors.Is(err, ErrTokenExpired):
		return deny(ReasonExpired)
	case err != nil:
		return deny(ReasonMalformed)
	}
	box = strings.ToUpper(strings.TrimSpace(box))
	if box == "" {
		return deny(ReasonNoBox)
	}
	if c.BoxID != box {
		return deny(ReasonOtherBox)
	}
	if !c.Tier.Allows(f) {
		return deny(ReasonTier)
	}
	return nil
}
