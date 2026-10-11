package domain

import "time"

// TrafficCounters are a Wi-Fi client's cumulative byte counters as reported by
// the router. They restart from zero at every (re)association; LinkSecs is how
// long the current association has lasted, which reveals such restarts.
type TrafficCounters struct {
	Rx       uint64 // bytes received by the router from the device (upload)
	Tx       uint64 // bytes sent by the router to the device (download)
	LinkSecs uint64
}

// Total is upload + download.
func (c TrafficCounters) Total() uint64 { return c.Rx + c.Tx }

// TrafficDelta returns the bytes consumed between two samples of the same
// device. A shorter association or a counter that went backwards means the
// device reconnected in between, so the whole current counter is new traffic.
func TrafficDelta(prev, cur TrafficCounters) uint64 {
	if cur.LinkSecs < prev.LinkSecs || cur.Rx < prev.Rx || cur.Tx < prev.Tx {
		return cur.Total()
	}
	return (cur.Rx - prev.Rx) + (cur.Tx - prev.Tx)
}

// Usage is a device's consumption over one billing period.
type Usage struct {
	MAC    MAC    `json:"mac"`
	Period string `json:"period"`
	Bytes  uint64 `json:"bytes"`
}

// BillingPeriod is the month a moment belongs to, e.g. "2026-10".
func BillingPeriod(t time.Time) string { return t.Format("2006-01") }
