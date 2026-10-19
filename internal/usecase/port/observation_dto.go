package port

import (
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// ObservationView is one poll's view of one station, in the JSON form the API
// serves and the relay signs into a snapshot (RFC-005 H2).
//
// It is a DTO rather than domain.Observation itself for two reasons: the
// domain type carries no JSON tags, and this one has to survive a round trip
// through a signed snapshot, so its names are part of a wire contract the hub
// decodes. Fields the router did not report are omitted, never guessed.
type ObservationView struct {
	MAC        string       `json:"mac"`
	At         time.Time    `json:"at"`
	IPv4       string       `json:"ipv4,omitempty"`
	Hostname   string       `json:"hostname,omitempty"`
	SSID       string       `json:"ssid,omitempty"`
	Band       string       `json:"band,omitempty"`
	Associated bool         `json:"associated"`
	AssocSince *time.Time   `json:"assoc_since,omitempty"`
	Lease      *LeaseView   `json:"lease,omitempty"`
	Radio      *RadioView   `json:"radio,omitempty"`
	Traffic    *TrafficView `json:"traffic,omitempty"`
}

// LeaseView is the station's DHCPv4 lease at the time of the poll.
type LeaseView struct {
	Seq       int       `json:"seq"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Port      string    `json:"port,omitempty"`
}

// RadioView is what the station's Wi-Fi chip negotiated on one band.
type RadioView struct {
	Standard    string `json:"standard,omitempty"`
	BTM         bool   `json:"btm"`
	RRM         bool   `json:"rrm"`
	Security    string `json:"security,omitempty"`
	MaxLinkMbps int    `json:"max_link_mbps,omitempty"`
}

// TrafficView are the station's cumulative byte counters. They restart at
// every (re)association, which LinkSecs reveals, so a consumer computes
// deltas with domain.TrafficDelta rather than subtracting blindly.
type TrafficView struct {
	Rx       uint64 `json:"rx"`
	Tx       uint64 `json:"tx"`
	LinkSecs uint64 `json:"link_secs"`
}

// NewObservationView renders an observation for the wire.
func NewObservationView(o domain.Observation) ObservationView {
	v := ObservationView{
		MAC:        o.MAC.String(),
		At:         o.At.UTC(),
		IPv4:       o.IPv4.String(),
		Hostname:   o.Hostname,
		SSID:       o.SSID,
		Band:       o.Band,
		Associated: o.Associated,
	}
	if !o.AssocSince.IsZero() {
		t := o.AssocSince.UTC()
		v.AssocSince = &t
	}
	if o.Lease != nil {
		v.Lease = &LeaseView{
			Seq: o.Lease.Seq, GrantedAt: o.Lease.GrantedAt.UTC(),
			ExpiresAt: o.Lease.ExpiresAt.UTC(), Port: o.Lease.Port,
		}
	}
	if o.Radio.Known() {
		v.Radio = &RadioView{
			Standard: o.Radio.Standard, BTM: o.Radio.BTM, RRM: o.Radio.RRM,
			Security: o.Radio.Security, MaxLinkMbps: o.Radio.MaxLinkMbps,
		}
	}
	return v
}

// NewObservationViews renders a whole poll, counters merged in when the caller
// read them. A MAC with no counters keeps none: an absent counter and a zero
// one are different facts.
func NewObservationViews(obs []domain.Observation, traffic map[domain.MAC]domain.TrafficCounters) []ObservationView {
	out := make([]ObservationView, 0, len(obs))
	for _, o := range obs {
		v := NewObservationView(o)
		if c, ok := traffic[o.MAC]; ok {
			v.Traffic = &TrafficView{Rx: c.Rx, Tx: c.Tx, LinkSecs: c.LinkSecs}
		}
		out = append(out, v)
	}
	return out
}

// Observation parses the view back into the domain type, which is what the
// hub's recognition works on. An unparseable MAC is the only hard error: it is
// the station's identity, and a station without one cannot be reasoned about.
func (v ObservationView) Observation() (domain.Observation, error) {
	mac, err := domain.ParseMAC(v.MAC)
	if err != nil {
		return domain.Observation{}, err
	}
	// A malformed address is dropped rather than refused: the MAC still
	// identifies the station, and the IP is only ever an attribute.
	ip, _ := domain.ParseIP(v.IPv4)
	o := domain.Observation{
		MAC: mac, At: v.At, IPv4: ip, Hostname: v.Hostname,
		SSID: v.SSID, Band: v.Band, Associated: v.Associated,
	}
	if v.AssocSince != nil {
		o.AssocSince = *v.AssocSince
	}
	if v.Lease != nil {
		o.Lease = &domain.Lease{
			Seq: v.Lease.Seq, GrantedAt: v.Lease.GrantedAt,
			ExpiresAt: v.Lease.ExpiresAt, Port: v.Lease.Port,
		}
	}
	if v.Radio != nil {
		o.Radio = domain.RadioCaps{
			Standard: v.Radio.Standard, BTM: v.Radio.BTM, RRM: v.Radio.RRM,
			Security: v.Radio.Security, MaxLinkMbps: v.Radio.MaxLinkMbps,
		}
	}
	return o, nil
}

// TrafficCounters returns the counters the view carries, if any.
func (v ObservationView) TrafficCounters() (domain.TrafficCounters, bool) {
	if v.Traffic == nil {
		return domain.TrafficCounters{}, false
	}
	return domain.TrafficCounters{Rx: v.Traffic.Rx, Tx: v.Traffic.Tx, LinkSecs: v.Traffic.LinkSecs}, true
}
