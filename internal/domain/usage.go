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
func TrafficDelta(prev, cur TrafficCounters) uint64 { return UsageDeltaOf(prev, cur).Total() }

// UsageDelta is what a device consumed between two samples, split by
// direction from the device's point of view.
type UsageDelta struct {
	Sent     uint64 // upload: what the router received from the device (Rx)
	Received uint64 // download: what the router sent to the device (Tx)
}

// Total is sent + received.
func (d UsageDelta) Total() uint64 { return d.Sent + d.Received }

// UsageDeltaOf is TrafficDelta split by direction.
func UsageDeltaOf(prev, cur TrafficCounters) UsageDelta {
	if cur.LinkSecs < prev.LinkSecs || cur.Rx < prev.Rx || cur.Tx < prev.Tx {
		return UsageDelta{Sent: cur.Rx, Received: cur.Tx}
	}
	return UsageDelta{Sent: cur.Rx - prev.Rx, Received: cur.Tx - prev.Tx}
}

// Usage is a device's consumption over one billing period.
type Usage struct {
	MAC    MAC    `json:"mac"`
	Period string `json:"period"`
	Bytes  uint64 `json:"bytes"`
	// Sent and Received split Bytes by direction. Usage recorded before the
	// split was kept has neither, so they may add up to less than Bytes.
	Sent     uint64 `json:"sent"`
	Received uint64 `json:"received"`
}

// BillingPeriod is the month a moment belongs to, e.g. "2026-10".
func BillingPeriod(t time.Time) string { return t.Format("2006-01") }

// UsageDay is the day a moment's consumption is stored under, e.g.
// "2026-10-31". Daily figures add up to the month's.
func UsageDay(t time.Time) string { return t.Format("2006-01-02") }

// UsageRetentionMonths is how long the daily consumption is kept: the
// current month and the twelve before it (the Max history, RFC-006).
const UsageRetentionMonths = 13

// UsageRetentionCutoff is the first day still kept at t ("YYYY-MM-DD"):
// days before it are dropped.
func UsageRetentionCutoff(t time.Time) string {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	return UsageDay(first.AddDate(0, -(UsageRetentionMonths - 1), 0))
}

// PeriodsBack is the n months ending with t's, oldest first.
func PeriodsBack(t time.Time, n int) []string {
	if n <= 0 {
		return []string{}
	}
	// From the first of the month, so that going back from a 31st never
	// overflows into the next month.
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = BillingPeriod(first.AddDate(0, i-(n-1), 0))
	}
	return out
}

// DeviceUsage is a device with its consumption over one period.
type DeviceUsage struct {
	Device
	Bytes uint64
}
