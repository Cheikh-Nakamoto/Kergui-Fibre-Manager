// Package domain is the pure core of the hub's licensing module (RFC-006,
// "Freemium app with Pro subscriptions"): the Free / Basic / Max tiers and
// what each unlocks, the prepaid pass catalogue, the subscription one
// account holds per box, the one-off trial, box transfers, the expiry
// reminder and the Ed25519 entitlement token the app verifies offline.
//
// Nothing here touches Postgres, HTTP or the clock: every rule takes "now"
// as an argument, so it is tested with plain values and the use cases decide
// where time comes from.
package domain
