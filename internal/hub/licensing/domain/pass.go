package domain

import (
	"errors"
	"time"
)

// Term is how long a pass lasts, as sold.
type Term string

const (
	Term1Month   Term = "1m"
	Term3Months  Term = "3m"
	Term12Months Term = "12m"
)

// Pass is a prepaid product: a tier for a number of days, at a price in
// whole francs CFA (XOF has no minor unit). Mobile money has no reliable
// recurring debit, so there is no renewal: each pass is bought (RFC-006,
// Passes, not recurring billing).
type Pass struct {
	Code     string // product code, e.g. "max_3m"; what a checkout names
	Tier     Tier
	Term     Term
	Days     int
	PriceXOF int64
}

// Duration is how long the pass adds to a subscription.
func (p Pass) Duration() time.Duration { return time.Duration(p.Days) * 24 * time.Hour }

// ErrUnknownPass is a product this hub does not sell.
var ErrUnknownPass = errors.New("licensing: unknown pass")

// catalogue is the RFC-006 price list. A month is 30 days, as the RFC says.
// The RFC gives 3 and 12 months no day count: 3 months is 3 × 30 = 90 days,
// and 12 months is a calendar year, 365 days, rather than 360 — someone who
// buys "a year" on 1 March expects it to last to the next 1 March, and the
// five days cost less than the support conversation.
var catalogue = []Pass{
	{Code: "basic_1m", Tier: TierBasic, Term: Term1Month, Days: 30, PriceXOF: 650},
	{Code: "basic_3m", Tier: TierBasic, Term: Term3Months, Days: 90, PriceXOF: 1850},
	{Code: "basic_12m", Tier: TierBasic, Term: Term12Months, Days: 365, PriceXOF: 6500},
	{Code: "max_1m", Tier: TierMax, Term: Term1Month, Days: 30, PriceXOF: 2000},
	{Code: "max_3m", Tier: TierMax, Term: Term3Months, Days: 90, PriceXOF: 5700},
	{Code: "max_12m", Tier: TierMax, Term: Term12Months, Days: 365, PriceXOF: 20000},
}

// TrialPass is the 7 days of Max a phone number and a box get once. It is
// granted, never sold, so it is not in the catalogue and has no product code
// a checkout could name.
var TrialPass = Pass{Code: "max_trial", Tier: TierMax, Days: 7}

// Catalogue lists the passes on sale. The slice is the caller's.
func Catalogue() []Pass { return append([]Pass(nil), catalogue...) }

// FindPass is the pass of a tier for a term.
func FindPass(tier Tier, term Term) (Pass, error) {
	for _, p := range catalogue {
		if p.Tier == tier && p.Term == term {
			return p, nil
		}
	}
	return Pass{}, ErrUnknownPass
}

// PassByCode is the pass a product code names.
func PassByCode(code string) (Pass, error) {
	for _, p := range catalogue {
		if p.Code == code {
			return p, nil
		}
	}
	return Pass{}, ErrUnknownPass
}
