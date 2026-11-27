package handler_test

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The privacy policy is the page the Play Console links to (#37): public,
// static, and it must say what the Data safety form declares.
func TestPrivacyPage(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.srv.URL + "/licensing/v1/privacy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Error("the privacy page sets a cookie")
	}

	// The CSP names the hash of the page's own style, so it renders styled
	// and nothing else can run.
	m := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no <style> in the page")
	}
	sum := sha256.Sum256([]byte(m[1]))
	want := "style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, want) || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q, want %q and default-src 'none'", csp, want)
	}
	if strings.Contains(body, "<script") {
		t.Error("the privacy page has a script")
	}

	// What the Data safety form declares (docs/mobile/play-data-safety.md).
	for _, s := range []string{
		"numéro de téléphone", "WhatsApp", "hub", "numéro de série de votre box",
		"chiffré", "Keystore", "jeton de droits", "créées sur le téléphone",
		"Aucune publicité", `id="suppression"`, "mailto:",
		// The publisher and its contact (owner's decision, #37).
		"Éditeur : Kergui", "mailto:feppdougou@gmail.com",
		// The deletion from the app (#37) and what it erases.
		"Supprimer mon compte", "SUPPRIMER", "sauvegarde", "demandes au support",
		"autres membres",
		// The relays, phone or PC (#45, #53, #90): what they send the hub
		// and why, what never leaves, the in-app disclosure, the permanent
		// notification and how to stop it.
		`id="relais"`, "téléphone Android", "ordinateur",
		"instantanés de la liste des appareils", "sans leurs mots de passe",
		"numéro de série de la box", "foyer du propriétaire", "sauvegarde du foyer",
		"« Accepter »", "service de premier plan", "« Relais Kergui »",
		"Arrêter le relais", "jamais en arrière-plan",
		// Push tokens (#49), with an account only.
		"identifiant de notification",
	} {
		if !strings.Contains(body, s) {
			t.Errorf("the privacy page does not say %q", s)
		}
	}

	// Linked from the Play listing: no price, offer or purchase wording
	// (the pattern of mobile/scripts/check-play-bundle.sh).
	payment := regexp.MustCompile(`NabooPay|Wave|Orange|ZTE|FCFA|XOF|prix|abonne|payer|acheter|achat|/buy`)
	if hit := payment.FindString(body); hit != "" {
		t.Errorf("the privacy page says %q", hit)
	}
}
