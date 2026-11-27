package domain

import "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"

// The tiers and the Feature -> Tier table live in internal/entitlement, the
// stdlib-only package the mobile core shares with the hub: one table decides
// both what a pass sells and what the app's core lets through. The names
// below keep the hub's code reading in its own vocabulary.

// Tier is a subscription level (RFC-006, Tiers): Free, Basic, Max.
type Tier = entitlement.Tier

const (
	TierFree  = entitlement.TierFree
	TierBasic = entitlement.TierBasic
	TierMax   = entitlement.TierMax
)

// ErrUnknownTier is a tier name this hub does not sell.
var ErrUnknownTier = entitlement.ErrUnknownTier

// ParseTier reads a tier name, forgiving about case and spacing.
func ParseTier(s string) (Tier, error) { return entitlement.ParseTier(s) }

// Feature is one gated action or screen of the app.
type Feature = entitlement.Feature

const (
	FeatureConnect                 = entitlement.FeatureConnect
	FeatureDeviceList              = entitlement.FeatureDeviceList
	FeatureRename                  = entitlement.FeatureRename
	FeatureJournal                 = entitlement.FeatureJournal
	FeatureAlerts                  = entitlement.FeatureAlerts
	FeatureCurrentMonthConsumption = entitlement.FeatureCurrentMonthConsumption
	FeatureNetworkOn               = entitlement.FeatureNetworkOn
	FeatureUnblock                 = entitlement.FeatureUnblock
	FeatureRestoreWiFiAccess       = entitlement.FeatureRestoreWiFiAccess
	FeatureWiFiUnlock              = entitlement.FeatureWiFiUnlock

	FeatureBlock         = entitlement.FeatureBlock
	FeatureCutWiFiAccess = entitlement.FeatureCutWiFiAccess

	FeatureConsumptionHistory = entitlement.FeatureConsumptionHistory
	FeatureMonthlyFilter      = entitlement.FeatureMonthlyFilter
	FeatureSSIDManage         = entitlement.FeatureSSIDManage
	FeatureWiFiPassword       = entitlement.FeatureWiFiPassword
	FeatureAutoReject         = entitlement.FeatureAutoReject
	FeatureWiFiLock           = entitlement.FeatureWiFiLock
)

// MinTier is the lowest tier that unlocks f; false for an unknown feature.
func MinTier(f Feature) (Tier, bool) { return entitlement.MinTier(f) }
