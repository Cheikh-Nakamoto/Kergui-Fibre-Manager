package entitlement

import (
	"errors"
	"strings"
)

// Tier is a subscription level. Max is a superset of Basic, which is a
// superset of Free (RFC-006, Tiers). It is a string so that it reads the same
// in the database, in the token and in a log.
type Tier string

const (
	TierFree  Tier = "free"
	TierBasic Tier = "basic"
	TierMax   Tier = "max"
)

// ErrUnknownTier is a tier name the hub does not sell.
var ErrUnknownTier = errors.New("licensing: unknown tier")

// rank orders the tiers; 0 means "not a tier".
func (t Tier) rank() int {
	switch t {
	case TierFree:
		return 1
	case TierBasic:
		return 2
	case TierMax:
		return 3
	}
	return 0
}

// Valid reports whether t is one of the three tiers.
func (t Tier) Valid() bool { return t.rank() > 0 }

// Includes reports whether everything other unlocks, t unlocks too.
func (t Tier) Includes(other Tier) bool {
	return t.Valid() && other.Valid() && t.rank() >= other.rank()
}

// Allows reports whether t unlocks the feature. An unknown feature or tier
// is refused: a paid action forgotten in the table fails closed.
func (t Tier) Allows(f Feature) bool {
	min, ok := featureTier[f]
	return ok && t.Includes(min)
}

// ParseTier reads a tier name, forgiving about case and spacing.
func ParseTier(s string) (Tier, error) {
	t := Tier(strings.ToLower(strings.TrimSpace(s)))
	if !t.Valid() {
		return "", ErrUnknownTier
	}
	return t, nil
}

// Feature is one gated action or screen of the app.
type Feature string

const (
	// Free forever: they create the need to block, or give access back.
	FeatureConnect                 Feature = "connect"
	FeatureDeviceList              Feature = "device_list"
	FeatureRename                  Feature = "rename"
	FeatureJournal                 Feature = "journal"
	FeatureAlerts                  Feature = "alerts"
	FeatureCurrentMonthConsumption Feature = "current_month_consumption"
	FeatureNetworkOn               Feature = "network_on"
	FeatureUnblock                 Feature = "unblock"
	FeatureRestoreWiFiAccess       Feature = "restore_wifi_access"
	// FeatureWiFiUnlock is « Ouvrir mon Wi-Fi » (RFC-007 D8): free, even
	// with a pass that lapsed while the Wi-Fi was closed.
	FeatureWiFiUnlock Feature = "wifi_unlock"

	// Basic.
	FeatureBlock         Feature = "block"
	FeatureCutWiFiAccess Feature = "cut_wifi_access"

	// Max.
	FeatureConsumptionHistory Feature = "consumption_history"
	FeatureMonthlyFilter      Feature = "monthly_filter"
	FeatureSSIDManage         Feature = "ssid_manage"
	FeatureWiFiPassword       Feature = "wifi_password"
	FeatureAutoReject         Feature = "auto_reject"
	// FeatureWiFiLock is « Fermer mon Wi-Fi », « Ajouter un nouvel
	// appareil » and its « Encore 5 min » (RFC-007 D8).
	FeatureWiFiLock Feature = "wifi_lock"
)

// featureTier is the RFC-006 tier table: the lowest tier that unlocks each
// feature. The app's mobile/src/licensing/gate.ts mirrors it; keep the two in
// step (TestFeatureTableMatchesApp checks it).
var featureTier = map[Feature]Tier{
	FeatureConnect:                 TierFree,
	FeatureDeviceList:              TierFree,
	FeatureRename:                  TierFree,
	FeatureJournal:                 TierFree,
	FeatureAlerts:                  TierFree,
	FeatureCurrentMonthConsumption: TierFree,
	FeatureNetworkOn:               TierFree,
	FeatureUnblock:                 TierFree,
	FeatureRestoreWiFiAccess:       TierFree,
	FeatureWiFiUnlock:              TierFree,

	FeatureBlock:         TierBasic,
	FeatureCutWiFiAccess: TierBasic,

	FeatureConsumptionHistory: TierMax,
	FeatureMonthlyFilter:      TierMax,
	FeatureSSIDManage:         TierMax,
	FeatureWiFiPassword:       TierMax,
	FeatureAutoReject:         TierMax,
	FeatureWiFiLock:           TierMax,
}

// MinTier is the lowest tier that unlocks f; false for an unknown feature.
func MinTier(f Feature) (Tier, bool) {
	t, ok := featureTier[f]
	return t, ok
}

// Free reports whether f is free forever: no token is needed for it.
func Free(f Feature) bool {
	t, ok := featureTier[f]
	return ok && t == TierFree
}
