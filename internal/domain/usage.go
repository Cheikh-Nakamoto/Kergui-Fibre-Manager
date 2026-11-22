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

// DeviceUsage is a device with its consumption over one period.
type DeviceUsage struct {
	Device
	Bytes uint64
}
