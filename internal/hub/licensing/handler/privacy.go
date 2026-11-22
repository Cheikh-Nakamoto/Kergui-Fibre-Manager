package handler

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"net/http"
)

// The privacy policy of the Play app (#37), at a stable public URL that the
// Play Console's "Privacy policy" field and the Data safety form point to:
// <hub>/licensing/v1/privacy. It is a fixed page, the same for everyone, so
// it is embedded as is; docs/mobile/release-playstore.md says what it must
// cover and when to update it. Like the rest of the Play listing it names no
// price and links to no purchase page.

// PrivacyPath is the public privacy policy page.
const PrivacyPath = Prefix + "privacy"

//go:embed privacy.html
var privacyHTML []byte

// privacyCSP allows the page's one inline style by its hash and nothing else.
var privacyCSP = "default-src 'none'; style-src '" + styleHash(privacyHTML) + "'; " +
	"form-action 'none'; frame-ancestors 'none'; base-uri 'none'"

// styleHash is the CSP source of the first <style> element's content.
func styleHash(page []byte) string {
	_, rest, ok := bytes.Cut(page, []byte("<style>"))
	if !ok {
		return "none"
	}
	css, _, _ := bytes.Cut(rest, []byte("</style>"))
	sum := sha256.Sum256(css)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func (h *Handler) privacy(w http.ResponseWriter, _ *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	// Public and the same for everyone: a proxy may keep it for an hour.
	hd.Set("Cache-Control", "public, max-age=3600")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Content-Security-Policy", privacyCSP)
	_, _ = w.Write(privacyHTML)
}
