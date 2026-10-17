package domain

import (
	"fmt"
	"sort"
	"time"
)

// DeviceID is the permanent internal identity of one device ("DEVICE-000042").
// It never changes when the device's IP or name do; a merge confirmed by the
// admin keeps the surviving ID and records the other as an alias.
type DeviceID string

// FormatDeviceID renders the n-th device identity.
func FormatDeviceID(n int) DeviceID { return DeviceID(fmt.Sprintf("DEVICE-%06d", n)) }

// KnownDevice is one device of the registry with every observation of every
// MAC bound to it (bound because it was seen, or because the admin confirmed).
type KnownDevice struct {
	ID           DeviceID
	Observations []Observation
}

// Strength orders evidence by what it may decide. It is ordinal on purpose:
// nothing here is a calibrated probability.
type Strength int

const (
	// Descriptive evidence is shown to the admin and never decides anything.
	Descriptive Strength = iota
	// Corroborating evidence can support a proposal, never an automatic link.
	Corroborating
	// Decisive evidence links a MAC to a device on its own.
	Decisive
)

// EvidenceKind names one rule of the recognition.
type EvidenceKind string

const (
	EvSameMAC       EvidenceKind = "same-mac"
	EvHostname      EvidenceKind = "same-hostname"
	EvHandoff       EvidenceKind = "handoff"
	EvCoPresent     EvidenceKind = "co-present"
	EvRadioMismatch EvidenceKind = "radio-mismatch"
	EvMACChange     EvidenceKind = "mac-change"
)

// Evidence is one fact for (Against == false) or against a MAC belonging to a
// known device.
type Evidence struct {
	Kind     EvidenceKind
	Strength Strength
	Against  bool
	Detail   string
}

// Outcome is what recognition decides for a MAC.
type Outcome int

const (
	// Unassigned: the MAC starts a new provisional device; the admin may
	// merge it by hand later.
	Unassigned Outcome = iota
	// Proposed: corroborating evidence only; the admin confirms or rejects.
	Proposed
	// Associated: the MAC is already bound to the device.
	Associated
)

func (o Outcome) String() string {
	return [...]string{"unassigned", "proposed", "associated"}[o]
}

// Candidate is a known device with the evidence collected for the MAC.
type Candidate struct {
	Device   DeviceID
	Evidence []Evidence
}

func (c Candidate) has(k EvidenceKind) bool {
	for _, e := range c.Evidence {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// Decision is the result of Recognise.
type Decision struct {
	Outcome Outcome
	Device  DeviceID // Associated only
	// Candidates: the proposals (Proposed), or, for Unassigned, the devices
	// with a single hint, shown so the admin can still merge by hand.
	Candidates []Candidate
}

// RecognitionRules are the thresholds. They are durations, not weights.
type RecognitionRules struct {
	// HandoffWindow: a new MAC first seen at most this long after a device was
	// last seen counts as a handoff.
	HandoffWindow time.Duration
	// MinOverlap: two MACs associated together at least this long are two
	// devices. Shorter overlaps can be one phone switching networks.
	MinOverlap time.Duration
	// SamePoll: observations closer than this belong to the same poll.
	SamePoll time.Duration
}

// DefaultRecognitionRules are the thresholds the tests document.
var DefaultRecognitionRules = RecognitionRules{
	HandoffWindow: 15 * time.Minute,
	MinOverlap:    2 * time.Minute,
	SamePoll:      5 * time.Second,
}

// Recognise decides which known device, if any, the observations of one MAC
// belong to. sharedNames are identity keys (IdentityKey) seen on two devices
// at once; they never count.
//
// Only an exact MAC binding associates automatically. A different MAC is
// never linked without the admin: a phone that presents a new private
// address may be doing so on purpose. Rules:
//  1. Same MAC as a device: Associated.
//  2. A device whose MACs were associated together with this one for at
//     least MinOverlap, or whose radio contradicts it on the same band, is
//     another device: it is never proposed.
//  3. Same distinctive host name AND a handoff: Proposed. A host name alone
//     is only a hint (Unassigned with a candidate). A timing handoff with no
//     name match is nothing — devices come and go all the time.
//  4. Otherwise: Unassigned.
func Recognise(obs []Observation, known []KnownDevice, sharedNames map[string]bool, rules RecognitionRules) Decision {
	var proposals, hints []Candidate
	for _, d := range known {
		c := evaluate(obs, d, sharedNames, rules)
		switch {
		case c.has(EvSameMAC):
			return Decision{Outcome: Associated, Device: d.ID, Candidates: []Candidate{c}}
		case c.has(EvCoPresent) || c.has(EvRadioMismatch):
			continue
		case c.has(EvHostname) && c.has(EvHandoff):
			proposals = append(proposals, c)
		case c.has(EvHostname):
			hints = append(hints, c)
		}
	}
	if len(proposals) > 0 {
		sort.SliceStable(proposals, func(i, j int) bool {
			return lastSeen(deviceObs(known, proposals[i].Device)).After(lastSeen(deviceObs(known, proposals[j].Device)))
		})
		return Decision{Outcome: Proposed, Candidates: proposals}
	}
	return Decision{Outcome: Unassigned, Candidates: hints}
}

func deviceObs(known []KnownDevice, id DeviceID) []Observation {
	for _, d := range known {
		if d.ID == id {
			return d.Observations
		}
	}
	return nil
}

func evaluate(obs []Observation, d KnownDevice, shared map[string]bool, r RecognitionRules) Candidate {
	c := Candidate{Device: d.ID}
	add := func(e Evidence) {
		if !c.has(e.Kind) {
			c.Evidence = append(c.Evidence, e)
		}
	}
	for _, o := range obs {
		for _, p := range d.Observations {
			for _, e := range pairEvidence(o, p, shared, r) {
				add(e)
			}
		}
	}
	if c.has(EvSameMAC) {
		return c
	}
	if gap, ok := handoff(obs, d.Observations, r); ok {
		add(Evidence{EvHandoff, Corroborating, false, fmt.Sprintf("new MAC first seen %s after the device was last seen", gap.Round(time.Second))})
	}
	if from, to, ok := macChange(obs, d.Observations); ok && len(c.Evidence) > 0 {
		add(Evidence{EvMACChange, Descriptive, false, from + " → " + to})
	}
	return c
}

// pairEvidence compares one observation of the MAC (o) with one observation
// of a known device (p).
func pairEvidence(o, p Observation, shared map[string]bool, r RecognitionRules) []Evidence {
	if o.MAC.Equal(p.MAC) {
		return []Evidence{{EvSameMAC, Decisive, false, fmt.Sprintf("MAC %s is already bound", o.MAC)}}
	}
	var out []Evidence
	if overlap, ok := coAssociated(o, p, r); ok {
		out = append(out, Evidence{EvCoPresent, Decisive, true,
			fmt.Sprintf("%s and %s were associated together for %s", o.MAC, p.MAC, overlap.Round(time.Second))})
	}
	if radioContradicts(o, p) {
		out = append(out, Evidence{EvRadioMismatch, Decisive, true, "different Wi-Fi standard or 802.11k/v support on " + o.Band})
	}
	k, ok := IdentityKey(o.Hostname)
	pk, pok := IdentityKey(p.Hostname)
	if ok && pok && k == pk && !shared[k] {
		out = append(out, Evidence{EvHostname, Corroborating, false, fmt.Sprintf("host name %q", o.Hostname)})
	}
	return out
}

// coAssociated reports whether two observations of the same poll show both
// MACs associated, and for how long they overlapped.
func coAssociated(a, b Observation, r RecognitionRules) (time.Duration, bool) {
	if !a.Associated || !b.Associated || absDur(a.At.Sub(b.At)) > r.SamePoll {
		return 0, false
	}
	since := a.AssocSince
	if b.AssocSince.After(since) {
		since = b.AssocSince
	}
	if since.IsZero() {
		return 0, false
	}
	overlap := a.At.Sub(since)
	return overlap, overlap >= r.MinOverlap
}

// radioContradicts: on one band, a Wi-Fi chip does not change its standard or
// its 802.11k/v support. Used only to keep two devices apart.
func radioContradicts(o, p Observation) bool {
	if !o.Radio.Known() || !p.Radio.Known() || o.Band == "" || o.Band != p.Band {
		return false
	}
	return o.Radio.Standard != p.Radio.Standard || o.Radio.BTM != p.Radio.BTM || o.Radio.RRM != p.Radio.RRM
}

// firstSeen is the earliest moment the observations prove the MAC existed.
func firstSeen(obs []Observation) time.Time {
	var t time.Time
	for _, o := range obs {
		for _, c := range []time.Time{o.At, o.AssocSince, leaseGranted(o)} {
			if !c.IsZero() && (t.IsZero() || c.Before(t)) {
				t = c
			}
		}
	}
	return t
}

// lastSeen is the latest moment the observations prove a MAC was active.
func lastSeen(obs []Observation) time.Time {
	var t time.Time
	for _, o := range obs {
		c := leaseGranted(o)
		if o.Associated {
			c = o.At
		}
		if c.After(t) {
			t = c
		}
	}
	return t
}

func leaseGranted(o Observation) time.Time {
	if o.Lease == nil {
		return time.Time{}
	}
	return o.Lease.GrantedAt
}

func handoff(obs, known []Observation, r RecognitionRules) (time.Duration, bool) {
	first, last := firstSeen(obs), lastSeen(known)
	if first.IsZero() || last.IsZero() {
		return 0, false
	}
	gap := first.Sub(last)
	return gap, gap >= 0 && gap <= r.HandoffWindow
}

func macChange(obs, known []Observation) (string, string, bool) {
	if len(obs) == 0 || len(known) == 0 {
		return "", "", false
	}
	kind := func(m MAC) string {
		if m.IsRandomized() {
			return "random"
		}
		return "universal"
	}
	last := known[0]
	for _, p := range known {
		if p.At.After(last.At) {
			last = p
		}
	}
	return kind(last.MAC), kind(obs[0].MAC), true
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
