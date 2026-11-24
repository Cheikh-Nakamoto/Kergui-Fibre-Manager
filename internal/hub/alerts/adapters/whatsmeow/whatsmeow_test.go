package whatsmeow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	"github.com/agile-factory/kernel/platform/audit"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

var at = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func pn(user string) types.JID  { return types.NewJID(user, types.DefaultUserServer) }
func lid(user string) types.JID { return types.NewJID(user, types.HiddenUserServer) }

func dm(sender types.JID, msg *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: sender, Sender: sender},
			ID:            "MSG1", Timestamp: at,
		},
		Message: msg,
	}
}

func text(s string) *waE2E.Message { return &waE2E.Message{Conversation: proto.String(s)} }

func reply(s, quoted string) *waE2E.Message {
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String(s),
		ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String(quoted)},
	}}
}

func noLID(context.Context, types.JID) (types.JID, error) { return types.JID{}, errors.New("unknown") }

func TestInbound_TextFromAPhoneNumber(t *testing.T) {
	got, err := Inbound(context.Background(), dm(pn("221771234567"), text("1")), noLID)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.InboundMessage{From: "+221771234567", Text: "1", ProviderID: "MSG1", At: at}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInbound_ReplyCarriesTheQuotedID(t *testing.T) {
	got, err := Inbound(context.Background(), dm(pn("221771234567"), reply("2", "ALERT-MSG")), noLID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "2" || got.QuotedProviderID != "ALERT-MSG" {
		t.Fatalf("got %+v, want text 2 quoting ALERT-MSG", got)
	}
}

func TestInbound_LIDSenderResolvedToItsPhone(t *testing.T) {
	// The phone number travels as the alternative address…
	e := dm(lid("123456789"), text("OK"))
	e.Info.SenderAlt = pn("221771234567")
	if got, err := Inbound(context.Background(), e, noLID); err != nil || got.From != "+221771234567" {
		t.Fatalf("alt address: got %+v, %v", got, err)
	}
	// …or only in whatsmeow's LID map.
	resolve := func(_ context.Context, j types.JID) (types.JID, error) {
		if j.User == "123456789" {
			return pn("221779999999"), nil
		}
		return types.JID{}, errors.New("unknown")
	}
	if got, err := Inbound(context.Background(), dm(lid("123456789"), text("OK")), resolve); err != nil || got.From != "+221779999999" {
		t.Fatalf("LID map: got %+v, %v", got, err)
	}
	// Neither: ignored, not an error worth more than a log line.
	if _, err := Inbound(context.Background(), dm(lid("555"), text("OK")), noLID); !errors.Is(err, domain.ErrIgnored) {
		t.Fatalf("unresolvable LID: err = %v, want ErrIgnored", err)
	}
}

func TestInbound_IgnoresWhatIsNotAPersonWriting(t *testing.T) {
	own := dm(pn("221771234567"), text("1"))
	own.Info.IsFromMe = true
	group := dm(pn("221771234567"), text("1"))
	group.Info.IsGroup = true
	group.Info.Chat = types.NewJID("120363", types.GroupServer)
	status := dm(pn("221771234567"), text("1"))
	status.Info.Chat = types.StatusBroadcastJID
	image := dm(pn("221771234567"), &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}})
	blank := dm(pn("221771234567"), text("   "))

	for name, e := range map[string]*events.Message{
		"own message": own, "group": group, "status": status, "image": image, "blank": blank,
	} {
		if _, err := Inbound(context.Background(), e, noLID); !errors.Is(err, domain.ErrIgnored) {
			t.Errorf("%s: err = %v, want ErrIgnored", name, err)
		}
	}
}

type fakeSession struct {
	loggedIn bool
	sentTo   types.JID
	sent     string
	err      error
}

func (f *fakeSession) IsLoggedIn() bool { return f.loggedIn }
func (f *fakeSession) SendText(_ context.Context, to types.JID, s string) (string, error) {
	f.sentTo, f.sent = to, s
	return "WAID-1", f.err
}
func (f *fakeSession) PNForLID(context.Context, types.JID) (types.JID, error) {
	return types.JID{}, errors.New("unknown")
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSend(t *testing.T) {
	f := &fakeSession{loggedIn: true}
	n := &Notifier{sess: f, log: quiet()}
	id, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "Kergui : …"})
	if err != nil || id != "WAID-1" {
		t.Fatalf("Send = %q, %v", id, err)
	}
	if f.sentTo != pn("221771234567") || f.sent != "Kergui : …" {
		t.Fatalf("sent %q to %s", f.sent, f.sentTo)
	}
}

func TestSend_NotLinkedIsNotActivated(t *testing.T) {
	n := &Notifier{sess: &fakeSession{}, log: quiet()}
	if _, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"}); !errors.Is(err, domain.ErrNotActivated) {
		t.Fatalf("err = %v, want ErrNotActivated", err)
	}
}

func TestHandleEvent_DeliversToTheAlertsModule(t *testing.T) {
	n := &Notifier{sess: &fakeSession{loggedIn: true}, log: quiet()}
	got := make(chan domain.InboundMessage, 1)
	n.OnInbound(func(_ context.Context, m domain.InboundMessage) { got <- m })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.runCtx = ctx

	n.handleEvent(dm(pn("221771234567"), text("STOP")))
	select {
	case m := <-got:
		if m.Text != "STOP" || m.From != "+221771234567" {
			t.Fatalf("delivered %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing delivered")
	}
}

func TestParseInbound_RefusesEveryRequest(t *testing.T) {
	n := &Notifier{log: quiet()}
	r := httptest.NewRequest(http.MethodPost, "/whatsapp/v1/inbound/x", strings.NewReader("{}"))
	if _, err := n.ParseInbound(r); !errors.Is(err, domain.ErrNotAuthentic) {
		t.Fatalf("err = %v, want ErrNotAuthentic", err)
	}
}

// --- admin routes ------------------------------------------------------------

type fakeAdminSession struct {
	st       Status
	linkedTo domain.Phone
	unlinked bool
}

func (f *fakeAdminSession) Status() Status { return f.st }
func (f *fakeAdminSession) Link(_ context.Context, p domain.Phone) (string, error) {
	if f.st.Linked {
		return "", ErrAlreadyLinked
	}
	f.linkedTo = p
	return "ABCD-EFGH", nil
}
func (f *fakeAdminSession) Unlink(context.Context) error {
	f.unlinked, f.st = true, Status{}
	return nil
}

func call(t *testing.T, a *Admin, method, path, body string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := kernelauth.WithUserID(r.Context(), "U1")
	if admin {
		ctx = kernelauth.WithPlatformRole(ctx, "ROLE_ADMIN")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r.WithContext(ctx))
	return w
}

func TestAdmin_OnlyPlatformAdmins(t *testing.T) {
	a := &Admin{Session: &fakeAdminSession{}, Number: "+221770000000"}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/whatsapp"},
		{http.MethodPost, "/api/v1/admin/whatsapp/link"},
		{http.MethodDelete, "/api/v1/admin/whatsapp"},
	} {
		if w := call(t, a, c.method, c.path, "", false); w.Code != http.StatusForbidden {
			t.Errorf("%s %s as a member = %d, want 403", c.method, c.path, w.Code)
		}
	}
}

func TestAdmin_LinkStatusUnlink(t *testing.T) {
	s := &fakeAdminSession{}
	log := &fakeLog{}
	a := &Admin{Session: s, Number: "+221770000000", Audit: log}

	w := call(t, a, http.MethodPost, "/api/v1/admin/whatsapp/link", "", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ABCD-EFGH") {
		t.Fatalf("link = %d %s", w.Code, w.Body)
	}
	if s.linkedTo != "+221770000000" {
		t.Fatalf("linked %q, want the configured number", s.linkedTo)
	}
	w = call(t, a, http.MethodPost, "/api/v1/admin/whatsapp/link", `{"number":"77 111 22 33"}`, true)
	if w.Code != http.StatusOK || s.linkedTo != "+221771112233" {
		t.Fatalf("link with a number = %d, linked %q", w.Code, s.linkedTo)
	}

	s.st = Status{Linked: true, Number: "+221771112233", Connected: true}
	if w = call(t, a, http.MethodPost, "/api/v1/admin/whatsapp/link", "", true); w.Code != http.StatusConflict {
		t.Fatalf("link while linked = %d, want 409", w.Code)
	}
	if w = call(t, a, http.MethodGet, "/api/v1/admin/whatsapp", "", true); !strings.Contains(w.Body.String(), `"linked":true`) {
		t.Fatalf("status = %s", w.Body)
	}
	if w = call(t, a, http.MethodDelete, "/api/v1/admin/whatsapp", "", true); w.Code != http.StatusOK || !s.unlinked {
		t.Fatalf("unlink = %d, unlinked %v", w.Code, s.unlinked)
	}

	// Both gestures are in the action log, the number masked; the refused
	// link is not.
	want := []string{string(actionlog.KindWhatsAppLinkRequested), string(actionlog.KindWhatsAppLinkRequested), string(actionlog.KindWhatsAppUnlinked)}
	if len(log.got) != len(want) {
		t.Fatalf("logged %d gestures, want %d: %+v", len(log.got), len(want), log.got)
	}
	for i, p := range log.got {
		summary, _ := p.Metadata[actionlog.MetaSummary].(string)
		if string(p.Action) != want[i] || p.TargetType != actionlog.TargetWhatsApp || summary == "" {
			t.Errorf("gesture %d = %+v, want %s", i, p, want[i])
		}
		if strings.Contains(summary, "771112233") {
			t.Errorf("gesture %d shows the whole number: %q", i, summary)
		}
	}
}

type fakeLog struct{ got []audit.LogEventParams }

func (f *fakeLog) Execute(_ context.Context, p audit.LogEventParams) { f.got = append(f.got, p) }
