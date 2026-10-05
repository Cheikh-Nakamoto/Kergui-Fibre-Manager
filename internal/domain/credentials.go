package domain

// Credentials are a router login. They are handled with care everywhere: the
// String/GoString methods redact the password so it can never be leaked into a
// log line or a %v/%#v dump (brief §12). The plaintext password is readable only
// via the explicit Password field, by code that genuinely needs it (the adapter
// performing a login, the vault encrypting it at rest).
type Credentials struct {
	Username string
	Password string
}

// HasPassword reports whether a password is set.
func (c Credentials) HasPassword() bool { return c.Password != "" }

// String redacts the password.
func (c Credentials) String() string {
	return "Credentials{Username:" + c.Username + ", Password:<redacted>}"
}

// GoString redacts the password for the %#v verb.
func (c Credentials) GoString() string { return c.String() }
