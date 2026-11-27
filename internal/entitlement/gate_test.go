package entitlement

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const box = "ZTEG00000001"

var (
	freeFeatures  = []Feature{FeatureConnect, FeatureDeviceList, FeatureRename, FeatureJournal, FeatureAlerts, FeatureCurrentMonthConsumption, FeatureNetworkOn, FeatureUnblock, FeatureRestoreWiFiAccess, FeatureWiFiUnlock}
	basicFeatures = []Feature{FeatureBlock, FeatureCutWiFiAccess}
	maxFeatures   = []Feature{FeatureConsumptionHistory, FeatureMonthlyFilter, FeatureSSIDManage, FeatureWiFiPassword, FeatureAutoReject, FeatureWiFiLock}
)

func reason(err error) Reason {
	var ne *NotEntitledError
	if errors.As(err, &ne) {
		return ne.Reason
	}
	return ""
}

// TestAuthorizeByTier: no token unlocks only the free features, Basic adds
// block and cut Wi-Fi access, Max everything; past its expiry a token keeps
// its tier for the 3-day grace, then falls back to free.
func TestAuthorizeByTier(t *testing.T) {
	pub, priv := testKey(1)
	ks := keySet(t, pub)
	basic := testClaims()
	maxC := testClaims()
	maxC.Tier = TierMax
	basicTok, maxTok := mustSign(t, priv, basic), mustSign(t, priv, maxC)
	exp := basic.ExpiresAt

	for _, tc := range []struct {
		name               string
		token              string
		now                time.Time
		wantBasic, wantMax bool
		wantReason         Reason
	}{
		{name: "no token", now: t0, wantReason: ReasonNoToken},
		{name: "basic", token: basicTok, now: t0.Add(day), wantBasic: true, wantReason: ReasonTier},
		{name: "max", token: maxTok, now: t0.Add(day), wantBasic: true, wantMax: true},
		{name: "basic, expired within grace", token: basicTok, now: exp.Add(OfflineGrace - 1), wantBasic: true, wantReason: ReasonTier},
		{name: "max, expired within grace", token: maxTok, now: exp.Add(2 * day), wantBasic: true, wantMax: true},
		{name: "max, past grace", token: maxTok, now: exp.Add(OfflineGrace), wantReason: ReasonExpired},
	} {
		for _, f := range freeFeatures {
			if err := Authorize(ks, tc.token, box, f, tc.now); err != nil {
				t.Errorf("%s: free %s refused: %v", tc.name, f, err)
			}
		}
		check := func(fs []Feature, want bool) {
			for _, f := range fs {
				err := Authorize(ks, tc.token, box, f, tc.now)
				if (err == nil) != want {
					t.Errorf("%s: %s allowed = %v, want %v (%v)", tc.name, f, err == nil, want, err)
				}
				if err != nil && (!errors.Is(err, ErrNotEntitled) || reason(err) != tc.wantReason) {
					t.Errorf("%s: %s: %v, want not entitled (%s)", tc.name, f, err, tc.wantReason)
				}
			}
		}
		check(basicFeatures, tc.wantBasic)
		check(maxFeatures, tc.wantMax)
	}
}

// TestAuthorizeRejects: a token that does not verify with a trusted key, or
// is for another box, unlocks nothing paid.
func TestAuthorizeRejects(t *testing.T) {
	pub, priv := testKey(1)
	_, otherPriv := testKey(2)
	ks := keySet(t, pub)
	maxC := testClaims()
	maxC.Tier = TierMax
	tok := mustSign(t, priv, maxC)
	p, s, _ := strings.Cut(tok, ".")
	tampered := p + "." + strings.Repeat("A", len(s)-2) + "AA"
	otherBox := maxC
	otherBox.BoxID = "ZTEG99999999"

	for _, tc := range []struct {
		name, token, box string
		keys             KeySet
		want             Reason
	}{
		{"bad signature", tampered, box, ks, ReasonSignature},
		{"unknown key id", mustSign(t, otherPriv, maxC), box, ks, ReasonUnknownKey},
		{"another box serial", mustSign(t, priv, otherBox), box, ks, ReasonOtherBox},
		{"box serial unknown", tok, "", ks, ReasonNoBox},
		{"no trusted key", tok, box, KeySet{}, ReasonNoKey},
		{"garbage", "not-a-token", box, ks, ReasonMalformed},
	} {
		err := Authorize(tc.keys, tc.token, tc.box, FeatureBlock, t0)
		if !errors.Is(err, ErrNotEntitled) || reason(err) != tc.want {
			t.Errorf("%s: Authorize = %v, want not entitled (%s)", tc.name, err, tc.want)
		}
		// Giving access back never depends on the token.
		for _, f := range freeFeatures {
			if err := Authorize(tc.keys, tc.token, tc.box, f, t0); err != nil {
				t.Errorf("%s: free %s refused: %v", tc.name, f, err)
			}
		}
	}
	// The serial is compared the way the router adapter reports it.
	if err := Authorize(ks, tok, " zteg00000001 ", FeatureBlock, t0); err != nil {
		t.Errorf("lower-case serial: %v", err)
	}
}

func TestAuthorizeUnknownFeatureFailsClosed(t *testing.T) {
	pub, priv := testKey(1)
	maxC := testClaims()
	maxC.Tier = TierMax
	err := Authorize(keySet(t, pub), mustSign(t, priv, maxC), box, Feature("teleport"), t0)
	if reason(err) != ReasonUnknown {
		t.Errorf("unknown feature: %v", err)
	}
}

func TestNotEntitledError(t *testing.T) {
	err := Authorize(KeySet{}, "", box, FeatureWiFiPassword, t0)
	var ne *NotEntitledError
	if !errors.As(err, &ne) || ne.Feature != FeatureWiFiPassword || ne.Required != TierMax {
		t.Fatalf("Authorize = %#v", err)
	}
	if !strings.Contains(err.Error(), "wifi_password") {
		t.Errorf("message = %q", err.Error())
	}
}
