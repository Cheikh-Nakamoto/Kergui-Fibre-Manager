package domain

import (
	"errors"
	"testing"
)

// TestCatalogue is the RFC-006 price list, in whole francs CFA.
func TestCatalogue(t *testing.T) {
	for _, tc := range []struct {
		tier  Tier
		term  Term
		code  string
		days  int
		price int64
	}{
		{TierBasic, Term1Month, "basic_1m", 30, 650},
		{TierBasic, Term3Months, "basic_3m", 90, 1850},
		{TierBasic, Term12Months, "basic_12m", 365, 6500},
		{TierMax, Term1Month, "max_1m", 30, 2000},
		{TierMax, Term3Months, "max_3m", 90, 5700},
		{TierMax, Term12Months, "max_12m", 365, 20000},
	} {
		p, err := FindPass(tc.tier, tc.term)
		if err != nil {
			t.Errorf("FindPass(%s, %s) = %v", tc.tier, tc.term, err)
			continue
		}
		if p.Code != tc.code || p.Days != tc.days || p.PriceXOF != tc.price || p.Tier != tc.tier || p.Term != tc.term {
			t.Errorf("FindPass(%s, %s) = %+v", tc.tier, tc.term, p)
		}
		byCode, err := PassByCode(tc.code)
		if err != nil || byCode != p {
			t.Errorf("PassByCode(%q) = %+v, %v; want %+v", tc.code, byCode, err, p)
		}
	}
	if n := len(Catalogue()); n != 6 {
		t.Errorf("Catalogue() has %d passes, want 6", n)
	}
}

func TestCatalogueIsACopy(t *testing.T) {
	c := Catalogue()
	c[0].PriceXOF = 1
	if Catalogue()[0].PriceXOF == 1 {
		t.Error("Catalogue() hands out the shared list")
	}
}

func TestUnknownPass(t *testing.T) {
	for _, tc := range []struct {
		tier Tier
		term Term
	}{
		{TierFree, Term1Month},
		{TierMax, Term("6m")},
		{Tier("gold"), Term1Month},
	} {
		if _, err := FindPass(tc.tier, tc.term); !errors.Is(err, ErrUnknownPass) {
			t.Errorf("FindPass(%s, %s) = %v, want ErrUnknownPass", tc.tier, tc.term, err)
		}
	}
	for _, code := range []string{"", "max", "max_6m", "trial", "free_1m"} {
		if _, err := PassByCode(code); !errors.Is(err, ErrUnknownPass) {
			t.Errorf("PassByCode(%q) = %v, want ErrUnknownPass", code, err)
		}
	}
}

// TestTrialPassNotForSale: the trial is a grant, never something a checkout
// can be opened for.
func TestTrialPassNotForSale(t *testing.T) {
	if TrialPass.Tier != TierMax || TrialPass.Days != 7 || TrialPass.PriceXOF != 0 {
		t.Errorf("TrialPass = %+v", TrialPass)
	}
	if _, err := PassByCode(TrialPass.Code); !errors.Is(err, ErrUnknownPass) {
		t.Errorf("the trial can be looked up as a product: %v", err)
	}
}
