package domain

import "time"

// Router is a managed router instance in the local inventory. Credentials are NOT
// part of this entity — they live only in the credential vault (brief §12).
type Router struct {
	ID        string
	Name      string
	BaseURL   string // e.g. "http://192.168.1.1"
	AdapterID string // which RouterPort implementation handles it (e.g. "zte_f660")
	Vendor    string
	Model     string
	CreatedAt time.Time
}
