package entitlement

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestAllows is the RFC-006 tier table. Anything that gives access back is
// free forever, so a user is never locked out by their own rule.
func TestAllows(t *testing.T) {
	for _, tc := range []struct {
		feature          Feature
		free, basic, max bool
	}{
		{FeatureConnect, true, true, true},
		{FeatureDeviceList, true, true, true},
		{FeatureRename, true, true, true},
		{FeatureJournal, true, true, true},
		{FeatureAlerts, true, true, true},
		{FeatureCurrentMonthConsumption, true, true, true},
		{FeatureNetworkOn, true, true, true},
		{FeatureUnblock, true, true, true},
		{FeatureRestoreWiFiAccess, true, true, true},
		{FeatureWiFiUnlock, true, true, true},
		{FeatureBlock, false, true, true},
		{FeatureCutWiFiAccess, false, true, true},
		{FeatureConsumptionHistory, false, false, true},
		{FeatureMonthlyFilter, false, false, true},
		{FeatureSSIDManage, false, false, true},
		{FeatureWiFiPassword, false, false, true},
		{FeatureAutoReject, false, false, true},
		{FeatureWiFiLock, false, false, true},
	} {
		for tier, want := range map[Tier]bool{TierFree: tc.free, TierBasic: tc.basic, TierMax: tc.max} {
			if got := tier.Allows(tc.feature); got != want {
				t.Errorf("%s.Allows(%s) = %v, want %v", tier, tc.feature, got, want)
			}
		}
	}
}

// TestAllowsUnknown: a feature the table does not know is refused, so a new
// paid action forgotten here fails closed rather than going out for free.
func TestAllowsUnknown(t *testing.T) {
	for _, tier := range []Tier{TierFree, TierBasic, TierMax, Tier("gold")} {
		if tier.Allows(Feature("teleport")) {
			t.Errorf("%s allows an unknown feature", tier)
		}
	}
	if Tier("gold").Allows(FeatureConnect) {
		t.Error("an unknown tier is treated as a real one")
	}
}

func TestTierIncludes(t *testing.T) {
	for _, tc := range []struct {
		t, other Tier
		want     bool
	}{
		{TierMax, TierBasic, true},
		{TierMax, TierFree, true},
		{TierBasic, TierMax, false},
		{TierBasic, TierBasic, true},
		{TierFree, TierBasic, false},
		{Tier("gold"), TierFree, false},
		{TierMax, Tier("gold"), false},
	} {
		if got := tc.t.Includes(tc.other); got != tc.want {
			t.Errorf("%s.Includes(%s) = %v, want %v", tc.t, tc.other, got, tc.want)
		}
	}
}

func TestParseTier(t *testing.T) {
	for in, want := range map[string]Tier{"free": TierFree, "basic": TierBasic, "max": TierMax, " Max ": TierMax} {
		got, err := ParseTier(in)
		if err != nil || got != want {
			t.Errorf("ParseTier(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "gold", "pro"} {
		if _, err := ParseTier(bad); err == nil {
			t.Errorf("ParseTier(%q) was accepted", bad)
		}
	}
}

func TestMinTier(t *testing.T) {
	if got, ok := MinTier(FeatureWiFiPassword); !ok || got != TierMax {
		t.Errorf("MinTier(wifi_password) = %s, %v", got, ok)
	}
	if got, ok := MinTier(FeatureUnblock); !ok || got != TierFree {
		t.Errorf("MinTier(unblock) = %s, %v", got, ok)
	}
	if _, ok := MinTier(Feature("teleport")); ok {
		t.Error("MinTier knows an unknown feature")
	}
}

// TestFeatureTableMatchesApp keeps the app's copy of the tier table
// (mobile/src/licensing/gate.ts) in step with this one: the app greys out
// what the core would refuse, so the two must agree feature for feature.
func TestFeatureTableMatchesApp(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "mobile", "src", "licensing", "gate.ts"))
	if err != nil {
		t.Skipf("app source not available: %v", err)
	}
	app := map[Feature]Tier{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([a-z_]+): '(free|basic|max)',`).FindAllStringSubmatch(string(src), -1) {
		app[Feature(m[1])] = Tier(m[2])
	}
	if len(app) != len(featureTier) {
		t.Errorf("gate.ts has %d features, the core %d", len(app), len(featureTier))
	}
	for f, want := range featureTier {
		if app[f] != want {
			t.Errorf("gate.ts: %s = %q, the core says %q", f, app[f], want)
		}
	}
}
