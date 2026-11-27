package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/complaints"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// complaintsSummaryEvery is the e2e hub's summary interval (15 min in
// production).
const complaintsSummaryEvery = 2 * time.Second

// TestE2E_WhatsAppComplaints is issue #83 (parent #68, user stories 5, 6,
// 9, 10, 11, 12, 13, 14, 15, 16, 17, 19, 20): complaints on the Kergui
// WhatsApp number. « 1 » on an alert still blocks; a customer's message
// that is no alert reply becomes complaint n° N and is acknowledged
// through the WhatsApp queue, the next lines join it; « Merci » and STOP
// make none. After the (shortened) interval each admin number gets one
// summary with the counts by category and the complaints waiting for more
// than a day, spaced by the queue's pace, and no second one without new
// complaints. From an admin number, LISTE, VOIR, R and OK do what they say
// (R's text reaches the customer, a closed complaint takes no answer), and
// anything else gets the help; both writes are in the action log with the
// admin's number. The same commands from a customer's number are a
// complaint.
func TestE2E_WhatsAppComplaints(t *testing.T) {
	w := newWorldWith(t, failoverTimings, withSummaryInterval(complaintsSummaryEvery))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rnd := func(prefix string) alertsdomain.Phone {
		return alertsdomain.Phone("+221" + prefix + fmt.Sprintf("%07d", mathrand.IntN(10_000_000)))
	}
	adminA, adminB := rnd("70"), rnd("70")
	custC, custD, custE, custF, custG := rnd("77"), rnd("78"), rnd("76"), rnd("75"), rnd("77")

	// The shared database: no complaint of another test is open, none is
	// left to summarize, and no summary goes before the test says so.
	setSummaryClock := func(at time.Time) {
		execSQL(t, `INSERT INTO hub_settings (key, value) VALUES ('complaint_summary', jsonb_build_object('at', $1::timestamptz))
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, at.UTC())
	}
	setSummaryClock(time.Now().Add(time.Hour))
	execSQL(t, `UPDATE hub_complaints SET status = 'closed', closed_at = now() WHERE status <> 'closed'`)
	execSQL(t, `UPDATE hub_complaints SET summarized_at = now() WHERE summarized_at IS NULL`)
	admins, _ := json.Marshal([]string{string(adminA), string(adminB)})
	execSQL(t, `INSERT INTO hub_settings (key, value) VALUES ('complaint_admins', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, admins)
	t.Cleanup(func() { execSQL(t, `DELETE FROM hub_settings WHERE key = 'complaint_admins'`) })

	// C has a Kergui account with a Max pass on a box, for VOIR.
	box := "BOX-" + strings.TrimPrefix(string(custC), "+221")
	accountC := uuid.NewString()
	execSQL(t, `INSERT INTO hub_licensing_accounts (id, phone) VALUES ($1, $2)`, accountC, string(custC))
	execSQL(t, `INSERT INTO hub_licensing_subscriptions (account_id, box_id, tier, expires_at)
		VALUES ($1, $2, 'max', now() + interval '30 days')`, accountC, box)

	// 1. Regression: « 1 » on an alert still blocks the device.
	w.household(t, ctx)
	w.box.Join(mockrouter.Station{MAC: intruderMAC, Hostname: "TECNO-POP-10", IP: "192.168.1.77", AP: "DEV.WIFI.AP1"})
	alert := w.waitForAlert(t, ownerPhone, 30*time.Second)
	if answer := w.reply(t, ownerPhone, "1", alert.MessageID); !w.box.Blocked(intruderMAC) {
		t.Fatalf("« 1 » no longer blocks; the hub answered %q", answer)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_complaints WHERE phone = $1 AND status <> 'closed'`, string(ownerPhone)); n != 0 {
		t.Fatalf("an alert reply made %d complaint(s)", n)
	}
	// 2. A customer's message becomes complaint n° N, acknowledged.
	w.write(t, custC, "Bonjour, j'ai payé par Wave mais mon pass n'est pas actif")
	nC, idC := complaintOf(t, custC)
	if cat := str1(t, `SELECT category FROM hub_complaints WHERE id = $1`, idC); cat != complaints.CategoryPayment {
		t.Fatalf("category = %s, want paiement", cat)
	}
	if src := str1(t, `SELECT source FROM hub_complaints WHERE id = $1`, idC); src != complaints.SourceWhatsApp {
		t.Fatalf("source = %s", src)
	}
	ack := waitForText(t, w.notifier, custC, fmt.Sprintf("Plainte n° %d reçue", nC), 10*time.Second)
	if ack.AlertID != "" {
		t.Fatalf("the acknowledgement = %+v", ack)
	}
	// The next line joins it, without a second acknowledgement.
	w.write(t, custC, "Référence du paiement : WV-123")
	if n := countRows(t, `SELECT count(*) FROM hub_complaints WHERE phone = $1`, string(custC)); n != 1 {
		t.Fatalf("%d complaints for one customer, want 1", n)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_complaint_messages WHERE complaint_id = $1 AND channel = 'whatsapp'`, idC); n != 2 {
		t.Fatalf("%d messages on the complaint, want 2", n)
	}

	// Two more customers; E's complaint has been waiting for two days.
	w.write(t, custD, "Ma box ne marche plus depuis ce matin")
	nD, idD := complaintOf(t, custD)
	w.write(t, custE, "Paiement Orange Money pas reçu")
	nE, idE := complaintOf(t, custE)
	execSQL(t, `UPDATE hub_complaints SET created_at = now() - interval '2 days' WHERE id = $1`, idE)

	// « Merci » and STOP make no complaint.
	w.write(t, custF, "Merci 🙏")
	w.write(t, custF, "STOP")
	waitForText(t, w.notifier, custF, "C'est noté", 10*time.Second)
	if n := countRows(t, `SELECT count(*) FROM hub_complaints WHERE phone = $1`, string(custF)); n != 0 {
		t.Fatalf("« Merci » or STOP made %d complaint(s)", n)
	}

	// 3. One summary per admin after the interval, spaced by the pace. The
	// last summary went now: the next is due an interval later.
	summaryClock := time.Now()
	setSummaryClock(summaryClock)
	want := "3 nouvelles plaintes (2 paiement, 1 box), dont 1 en attente depuis plus d'un jour. Répondez LISTE pour les voir."
	sumA := waitForText(t, w.notifier, adminA, "nouvelles plaintes", 15*time.Second)
	sumB := waitForText(t, w.notifier, adminB, "nouvelles plaintes", 15*time.Second)
	if sumA.Text != want || sumB.Text != want {
		t.Fatalf("summary = %q / %q\nwant %q", sumA.Text, sumB.Text, want)
	}
	first, second := sumA, sumB
	if second.At.Before(first.At) {
		first, second = second, first
	}
	if first.At.Sub(summaryClock) < complaintsSummaryEvery {
		t.Fatalf("the summary came %v after the last one, the interval is %v", first.At.Sub(summaryClock), complaintsSummaryEvery)
	}
	if gap := second.At.Sub(first.At); gap < whatsAppSendInterval {
		t.Fatalf("the two admins' summaries are %v apart, want at least %v", gap, whatsAppSendInterval)
	}
	time.Sleep(2 * complaintsSummaryEvery)
	for _, a := range []alertsdomain.Phone{adminA, adminB} {
		if n := countSent(w.notifier, a, "nouvelle"); n != 1 {
			t.Fatalf("%d summaries to %s without new complaints, want 1", n, a.Masked())
		}
	}

	// 4. The admin's commands.
	list := w.command(t, adminA, "  liste ", "Plaintes ouvertes")
	for _, s := range []string{
		fmt.Sprintf("n° %d · %s · paiement", nC, custC.Masked()),
		fmt.Sprintf("n° %d · %s · box", nD, custD.Masked()),
		fmt.Sprintf("n° %d · %s · paiement · depuis 2 j", nE, custE.Masked()),
		"Bonjour, j'ai payé par Wave",
	} {
		if !strings.Contains(list, s) {
			t.Fatalf("LISTE lacks %q:\n%s", s, list)
		}
	}
	if strings.Contains(list, string(custC)) {
		t.Fatalf("LISTE shows a full number:\n%s", list)
	}
	if strings.Index(list, fmt.Sprintf("n° %d ", nE)) > strings.Index(list, fmt.Sprintf("n° %d ", nC)) {
		t.Fatalf("LISTE does not show the longest waiting first:\n%s", list)
	}

	view := w.command(t, adminA, fmt.Sprintf("voir #%d", nC), fmt.Sprintf("Plainte n° %d", nC))
	for _, s := range []string{string(custC), box, "Max jusqu'au", "Référence du paiement : WV-123", "Client :", fmt.Sprintf("R %d", nC)} {
		if !strings.Contains(view, s) {
			t.Fatalf("VOIR lacks %q:\n%s", s, view)
		}
	}

	answerText := "Votre pass est actif maintenant, merci de votre patience."
	done := w.command(t, adminA, fmt.Sprintf("R %d %s", nC, answerText), "Réponse envoyée")
	if !strings.Contains(done, fmt.Sprintf("n° %d", nC)) {
		t.Fatalf("R confirmed %q", done)
	}
	got := waitForText(t, w.notifier, custC, answerText, 10*time.Second)
	if !strings.Contains(got.Text, fmt.Sprintf("réponse à votre plainte n° %d", nC)) {
		t.Fatalf("the customer received %q", got.Text)
	}
	if st := str1(t, `SELECT status FROM hub_complaints WHERE id = $1`, idC); st != complaints.StatusAnswered {
		t.Fatalf("status after R = %s", st)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_complaint_messages WHERE complaint_id = $1 AND sender = 'admin' AND channel = 'whatsapp' AND body = $2`, idC, answerText); n != 1 {
		t.Fatalf("the answer is not in the thread")
	}
	waitForRow(t, 20*time.Second, `SELECT count(*) FROM audit_logs WHERE action = 'COMPLAINT_ANSWERED'
		AND target_id = $1 AND metadata->>'adminPhone' = $2 AND metadata->>'channel' = 'whatsapp'`, idC, string(adminA))

	// The customer answers back: the complaint opens again, acknowledged.
	w.write(t, custC, "Merci, c'est bon de mon côté")
	waitForText(t, w.notifier, custC, fmt.Sprintf("Message ajouté à votre plainte n° %d", nC), 10*time.Second)
	if st := str1(t, `SELECT status FROM hub_complaints WHERE id = $1`, idC); st != complaints.StatusOpen {
		t.Fatalf("status after the customer's answer = %s", st)
	}

	w.command(t, adminA, fmt.Sprintf("OK %d", nC), fmt.Sprintf("Plainte n° %d close", nC))
	if st := str1(t, `SELECT status FROM hub_complaints WHERE id = $1`, idC); st != complaints.StatusClosed {
		t.Fatalf("status after OK = %s", st)
	}
	waitForRow(t, 20*time.Second, `SELECT count(*) FROM audit_logs WHERE action = 'COMPLAINT_CLOSED'
		AND target_id = $1 AND metadata->>'adminPhone' = $2`, idC, string(adminA))
	w.command(t, adminB, fmt.Sprintf("ok %d", nC), "déjà close")
	w.command(t, adminB, fmt.Sprintf("R %d trop tard", nC), "est close")
	w.command(t, adminB, "VOIR 999999999", "Pas de plainte n° 999999999")
	help := w.command(t, adminB, "Bonjour", "Commandes plaintes")
	if help != complaints.HelpText {
		t.Fatalf("help = %q", help)
	}
	w.command(t, adminB, "VOIR", "Commandes plaintes")
	for _, a := range []alertsdomain.Phone{adminA, adminB} {
		if n := countRows(t, `SELECT count(*) FROM hub_complaints WHERE phone = $1`, string(a)); n != 0 {
			t.Fatalf("an admin's command made a complaint")
		}
	}

	// 5. The same commands from a customer's number are a complaint.
	before := countRows(t, `SELECT count(*) FROM hub_complaint_messages WHERE complaint_id = $1`, idD)
	w.write(t, custD, "LISTE")
	if after := countRows(t, `SELECT count(*) FROM hub_complaint_messages WHERE complaint_id = $1`, idD); after != before+1 {
		t.Fatalf("LISTE from a customer did not join their complaint (%d → %d messages)", before, after)
	}
	w.write(t, custG, fmt.Sprintf("OK %d", nD))
	nG, _ := complaintOf(t, custG)
	waitForText(t, w.notifier, custG, fmt.Sprintf("Plainte n° %d reçue", nG), 10*time.Second)
	if st := str1(t, `SELECT status FROM hub_complaints WHERE id = $1`, idD); st != complaints.StatusOpen {
		t.Fatalf("a customer's « OK n » closed complaint n° %d", nD)
	}
	// 6. A complaint told in the app a minute ago: the customer's first
	// WhatsApp line joins it and is acknowledged, though the app's line is
	// within the quiet window.
	custH := rnd("77")
	idH := uuid.NewString()
	execSQL(t, `INSERT INTO hub_complaints (id, source, category, phone) VALUES ($1, 'app', 'autre', $2)`, idH, string(custH))
	execSQL(t, `INSERT INTO hub_complaint_messages (complaint_id, sender, channel, body) VALUES ($1, 'customer', 'app', 'Une idée')`, idH)
	nH := int64(countRows(t, `SELECT number FROM hub_complaints WHERE id = $1`, idH))
	w.write(t, custH, "Bonjour, depuis hier soir Kergui ne trouve plus ma box. Que faire ?")
	waitForText(t, w.notifier, custH, fmt.Sprintf("Message ajouté à votre plainte n° %d", nH), 10*time.Second)
	if n := countRows(t, `SELECT count(*) FROM hub_complaint_messages WHERE complaint_id = $1 AND channel = 'whatsapp'`, idH); n != 1 {
		t.Fatalf("the WhatsApp line did not join the app's complaint (%d)", n)
	}

	time.Sleep(3 * whatsAppSendInterval)
	for _, p := range []alertsdomain.Phone{custD, custG} {
		if n := countSent(w.notifier, p, "Plaintes ouvertes"); n != 0 {
			t.Fatalf("a customer received the list")
		}
	}
}

// write delivers one inbound message, like send, without waiting for an
// answer: complaints answer through the paced WhatsApp queue. The webhook
// shares the unauthenticated routes' budget (one call a second past a
// burst of ten), so a 429 is waited out.
func (w *world) write(t *testing.T, phone alertsdomain.Phone, text string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"from": string(phone), "text": text})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodPost, w.hub.URL+"/whatsapp/v1/inbound/"+webhookSecret, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(console.SignatureHeader, console.Sign([]byte(webhookSecret), body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			return
		case resp.StatusCode == http.StatusTooManyRequests && time.Now().Before(deadline):
			time.Sleep(250 * time.Millisecond)
		default:
			t.Fatalf("the inbound webhook answered %d", resp.StatusCode)
		}
	}
}

// command sends an admin's command and returns the answer that contains
// want, the first one sent after the command.
func (w *world) command(t *testing.T, admin alertsdomain.Phone, text, want string) string {
	t.Helper()
	before := len(w.notifier.Sent())
	w.write(t, admin, text)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range w.notifier.Sent()[before:] {
			if s.To == admin && strings.Contains(s.Text, want) {
				return s.Text
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%q got no answer containing %q", text, want)
	return ""
}

// complaintOf is the customer's newest complaint: its number and id.
func complaintOf(t *testing.T, phone alertsdomain.Phone) (int64, string) {
	t.Helper()
	var n int64
	var id string
	if err := e2ePool(t).QueryRow(context.Background(),
		`SELECT number, id::text FROM hub_complaints WHERE phone = $1 ORDER BY created_at DESC LIMIT 1`, string(phone)).
		Scan(&n, &id); err != nil {
		t.Fatalf("no complaint for %s: %v", phone.Masked(), err)
	}
	return n, id
}

// str1 reads one text value.
func str1(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := e2ePool(t).QueryRow(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// countSent counts the messages to a number that contain text.
func countSent(n *console.Notifier, phone alertsdomain.Phone, text string) int {
	c := 0
	for _, s := range n.Sent() {
		if s.To == phone && strings.Contains(s.Text, text) {
			c++
		}
	}
	return c
}
