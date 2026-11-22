package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

const (
	instance = "kergui-pilote"
	// token is the instance's own token, the one chosen at
	// POST /instance/create. It is what KERGUI_HUB_EVOLUTION_APIKEY holds.
	token = "instance-token-0123456789"
	// globalKey is Evolution Go's GLOBAL_API_KEY: it opens the admin
	// routes only, and is refused on the instance ones.
	globalKey = "global-api-key"
)

// fakeEvolution reproduces what Evolution Go's own code does on the routes
// the adapter uses (pkg/routes/routes.go, pkg/middleware/auth_middleware.go,
// pkg/core/c0.go, pkg/instance and pkg/sendMessage handlers, main branch,
// read 2026-11-22):
//
//   - until the instance is activated in the Manager, every route answers
//     503 with code LICENSE_REQUIRED and the Manager's login URL;
//   - the instance routes take the instance token in the apikey header and
//     find the instance from it; any other key, GLOBAL_API_KEY included,
//     gets 401 {"error":"not authorized"};
//   - POST /send/text wants {"number","text"} and answers
//     {"message":"success","data":{"Info":{…,"ID":…},"Message":…}};
//   - GET /instance/status answers
//     {"message":"success","data":{"Connected":…,"LoggedIn":…,"Name":…}}.
type fakeEvolution struct {
	mu        sync.Mutex
	activated bool
	loggedIn  bool
	// lowercaseStatus answers the status keys in lower case, which the
	// adapter must read just as well.
	lowercaseStatus bool
	sent            []map[string]any
	paths           []string
}

func (f *fakeEvolution) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	writeJSON := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if !f.activated {
		writeJSON(http.StatusServiceUnavailable, map[string]string{
			"error":        "service not activated",
			"code":         "LICENSE_REQUIRED",
			"register_url": "http://" + r.Host + "/manager/login",
			"message":      "License required. Open the manager to activate your license.",
		})
		return
	}
	if r.Header.Get("apikey") != token {
		writeJSON(http.StatusUnauthorized, map[string]string{"error": "not authorized"})
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/send/text":
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") || json.Unmarshal(raw, &body) != nil {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "Invalid JSON format"})
			return
		}
		if s, _ := body["number"].(string); s == "" {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "phone number is required"})
			return
		}
		if s, _ := body["text"].(string); s == "" {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "message body is required"})
			return
		}
		f.sent = append(f.sent, body)
		writeJSON(http.StatusOK, map[string]any{"message": "success", "data": map[string]any{
			"Info": map[string]any{
				"Chat": body["number"].(string) + "@s.whatsapp.net", "IsFromMe": true,
				"ID": "3EB0C1", "Type": "text", "Timestamp": "2026-11-22T10:00:00Z",
			},
			"Message": map[string]any{"extendedTextMessage": map[string]any{"text": body["text"]}},
		}})
	case r.Method == http.MethodGet && r.URL.Path == "/instance/status":
		data := map[string]any{"Connected": f.loggedIn, "LoggedIn": f.loggedIn, "Name": "Kergui"}
		if f.lowercaseStatus {
			data = map[string]any{"connected": f.loggedIn, "loggedIn": f.loggedIn, "name": "Kergui"}
		}
		writeJSON(http.StatusOK, map[string]any{"message": "success", "data": data})
	default:
		writeJSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func newFake(t *testing.T, f *fakeEvolution, apiKey string) *Notifier {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	n, err := New(Config{BaseURL: srv.URL, Instance: instance, APIKey: apiKey})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func newNotifier(t *testing.T, h http.Handler) *Notifier {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	n, err := New(Config{BaseURL: srv.URL, Instance: instance, APIKey: token})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNew_RefusesHalfConfigured(t *testing.T) {
	for _, cfg := range []Config{
		{BaseURL: "", Instance: instance, APIKey: token},
		{BaseURL: "not a url", Instance: instance, APIKey: token},
		{BaseURL: "http://evolution:8080", Instance: "", APIKey: token},
		{BaseURL: "http://evolution:8080", Instance: instance, APIKey: ""},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) was accepted", cfg)
		}
	}
}

func TestDefaults_AreEvolutionGoRoutes(t *testing.T) {
	if DefaultSendPath != "/send/text" || DefaultStatusPath != "/instance/status" {
		t.Errorf("defaults = %q, %q; want Evolution Go's /send/text and /instance/status",
			DefaultSendPath, DefaultStatusPath)
	}
}

func TestSend(t *testing.T) {
	f := &fakeEvolution{activated: true, loggedIn: true}
	n := newFake(t, f, token)

	id, err := n.Send(context.Background(), "+221771234567",
		domain.OutboundMessage{Text: "Kergui : un nouvel appareil…", AlertID: "alert-1"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "3EB0C1" {
		t.Errorf("message id = %q, want data.Info.ID", id)
	}
	if len(f.sent) != 1 || f.paths[0] != "POST /send/text" {
		t.Fatalf("calls = %v", f.paths)
	}
	body := f.sent[0]
	// WhatsApp wants the digits, not the plus.
	if body["number"] != "221771234567" {
		t.Errorf("number = %v, want it without the leading plus", body["number"])
	}
	if s, _ := body["text"].(string); !strings.HasPrefix(s, "Kergui") {
		t.Errorf("text = %v", body["text"])
	}
	// The instance is the token's, not a field of the body: sending its
	// name would only be noise.
	if _, ok := body["instance"]; ok {
		t.Errorf("body carries an instance name: %v", body)
	}
}

// TestSend_ToleratesAnUnreadableAnswer: the answer's shape is not
// documented. A message that went out but whose id cannot be read is still
// sent; only the reply-to-this-alert shortcut is lost.
func TestSend_ToleratesAnUnreadableAnswer(t *testing.T) {
	for _, answer := range []string{`OK`, `{"message":"success"}`, `{"data":null}`} {
		n := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(answer))
		}))
		id, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"})
		if err != nil || id != "" {
			t.Errorf("answer %q: Send = %q, %v; want no id and no error", answer, id, err)
		}
	}
}

// TestSend_NotActivated: an instance not activated in its Manager answers
// 503 LICENSE_REQUIRED. That is a licensing state, not a hiccup, so the hub
// reports it as such, with the URL an administrator must open.
func TestSend_NotActivated(t *testing.T) {
	f := &fakeEvolution{activated: false}
	n := newFake(t, f, token)

	_, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"})
	if !errors.Is(err, domain.ErrNotActivated) {
		t.Fatalf("Send error = %v, want ErrNotActivated", err)
	}
	if !strings.Contains(err.Error(), "/manager/login") {
		t.Errorf("Send error = %v, want it to name the Manager's register URL", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error carries the token: %v", err)
	}
	if err := n.Status(context.Background()); !errors.Is(err, domain.ErrNotActivated) {
		t.Fatalf("Status error = %v, want ErrNotActivated", err)
	}
}

// TestSend_GlobalKeyIsRefused: Evolution Go finds the instance from its
// token, so GLOBAL_API_KEY in KERGUI_HUB_EVOLUTION_APIKEY is a
// misconfiguration the error must point at — without echoing either key.
func TestSend_GlobalKeyIsRefused(t *testing.T) {
	f := &fakeEvolution{activated: true, loggedIn: true}
	n := newFake(t, f, globalKey)

	_, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"})
	if err == nil {
		t.Fatal("a refused key was reported as a success")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Send error = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), globalKey) || strings.Contains(err.Error(), token) {
		t.Errorf("the error carries a key: %v", err)
	}
	if err := n.Status(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Status error = %v, want ErrUnauthorized", err)
	}
}

func TestSend_ProviderErrorIsReported(t *testing.T) {
	n := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"no active session found"}`))
	}))
	_, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("Send error = %v, want the status code", err)
	}
}

func TestStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake *fakeEvolution
		want error
	}{
		{"logged in", &fakeEvolution{activated: true, loggedIn: true}, nil},
		{"logged in, lower-case keys", &fakeEvolution{activated: true, loggedIn: true, lowercaseStatus: true}, nil},
		{"QR not scanned", &fakeEvolution{activated: true, loggedIn: false}, ErrNotConnected},
		{"QR not scanned, lower-case keys", &fakeEvolution{activated: true, lowercaseStatus: true}, ErrNotConnected},
		{"not activated", &fakeEvolution{}, domain.ErrNotActivated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newFake(t, tc.fake, token)
			err := n.Status(context.Background())
			if tc.want == nil && err != nil {
				t.Fatalf("Status = %v, want nil", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Status = %v, want %v", err, tc.want)
			}
			if got := tc.fake.paths[0]; got != "GET /instance/status" {
				t.Errorf("status call = %q", got)
			}
		})
	}
}

func TestStatus_UnreadableAnswerIsAnError(t *testing.T) {
	n := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>proxy</html>`))
	}))
	if err := n.Status(context.Background()); err == nil {
		t.Fatal("an answer that says nothing about the session was reported as healthy")
	}
}

// messageEvent is a MESSAGE webhook as Evolution Go builds it in
// pkg/whatsmeow/service/whatsmeow.go (myEventHandler, case *events.Message):
// "data" is whatsmeow's events.Message marshalled with encoding/json, so
// Info carries types.MessageInfo's untagged (PascalCase) fields and Message
// the protobuf's json tags; instanceToken, instanceId and instanceName are
// added before the call.
func messageEvent(mut func(ev map[string]any, info, message map[string]any)) string {
	info := map[string]any{
		"Chat": "221771234567@s.whatsapp.net", "Sender": "221771234567@s.whatsapp.net",
		"IsFromMe": false, "IsGroup": false, "AddressingMode": "pn",
		"SenderAlt": "123456789012345@lid", "RecipientAlt": "",
		"ID": "3EB0C2", "ServerID": 0, "Type": "text", "PushName": "Awa",
		"Timestamp": "2026-11-22T10:01:02Z", "Category": "", "Multicast": false,
	}
	message := map[string]any{
		"extendedTextMessage": map[string]any{
			"text": "1", "contextInfo": map[string]any{"stanzaID": "3EB0C1"},
		},
	}
	ev := map[string]any{
		"event": "Message",
		"data": map[string]any{
			"Info": info, "Message": message,
			"IsEphemeral": false, "IsViewOnce": false, "IsEdit": false,
		},
		"instanceToken": token,
		"instanceId":    "5d1a…",
		"instanceName":  instance,
	}
	if mut != nil {
		mut(ev, info, message)
	}
	raw, _ := json.Marshal(ev)
	return string(raw)
}

// inboundRequest builds a webhook call carrying one event.
func inboundRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/whatsapp/v1/inbound/secret", strings.NewReader(body))
}

func TestParseInbound(t *testing.T) {
	n := newNotifier(t, http.NotFoundHandler())

	msg, err := n.ParseInbound(inboundRequest(messageEvent(nil)))
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}
	if msg.From != "+221771234567" || msg.Text != "1" {
		t.Errorf("message = %+v", msg)
	}
	if msg.QuotedProviderID != "3EB0C1" {
		t.Errorf("the quoted message id was lost: %+v", msg)
	}
	want := time.Date(2026, 11, 22, 10, 1, 2, 0, time.UTC)
	if msg.ProviderID != "3EB0C2" || !msg.At.Equal(want) {
		t.Errorf("message = %+v", msg)
	}

	// A plain conversation message carries its text elsewhere, and a JID
	// may carry a device suffix.
	msg, err = n.ParseInbound(inboundRequest(messageEvent(func(_, info, message map[string]any) {
		info["Sender"] = "221771234567:12@s.whatsapp.net"
		delete(message, "extendedTextMessage")
		message["conversation"] = "STOP"
	})))
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}
	if msg.Text != "STOP" || msg.From != "+221771234567" || msg.QuotedProviderID != "" {
		t.Errorf("message = %+v, want STOP from the number without its device suffix", msg)
	}

	// A sender addressed by its LID: Evolution Go swaps in the phone JID
	// when it has one, but the number may still only be in SenderAlt.
	msg, err = n.ParseInbound(inboundRequest(messageEvent(func(_, info, _ map[string]any) {
		info["Sender"], info["Chat"] = "123456789012345@lid", "123456789012345@lid"
		info["SenderAlt"] = "221771234567@s.whatsapp.net"
	})))
	if err != nil || msg.From != "+221771234567" {
		t.Errorf("LID sender with a phone SenderAlt: %+v, %v", msg, err)
	}

	// A timestamp we cannot read is not a reason to drop the answer.
	msg, err = n.ParseInbound(inboundRequest(messageEvent(func(_, info, _ map[string]any) {
		info["Timestamp"] = "yesterday"
	})))
	if err != nil || msg.At.IsZero() || msg.Text != "1" {
		t.Errorf("unreadable timestamp: %+v, %v", msg, err)
	}
}

func TestParseInbound_Refusals(t *testing.T) {
	n := newNotifier(t, http.NotFoundHandler())
	for _, tc := range []struct {
		name string
		mut  func(ev, info, message map[string]any)
		raw  string
		want error
	}{
		{
			name: "another instance's traffic",
			mut:  func(ev, _, _ map[string]any) { ev["instanceName"] = "autre" },
			want: domain.ErrNotAuthentic,
		},
		{
			name: "another instance's token",
			mut:  func(ev, _, _ map[string]any) { ev["instanceToken"] = "someone-else" },
			want: domain.ErrNotAuthentic,
		},
		{
			name: "no token at all",
			mut:  func(ev, _, _ map[string]any) { delete(ev, "instanceToken") },
			want: domain.ErrNotAuthentic,
		},
		{
			name: "a message we sent ourselves",
			mut:  func(_, info, _ map[string]any) { info["IsFromMe"] = true },
			want: domain.ErrIgnored,
		},
		{
			name: "a group chat",
			mut: func(_, info, _ map[string]any) {
				info["Chat"], info["IsGroup"] = "2217712345-1600000000@g.us", true
			},
			want: domain.ErrIgnored,
		},
		{
			name: "a status broadcast",
			mut:  func(_, info, _ map[string]any) { info["Chat"] = "status@broadcast" },
			want: domain.ErrIgnored,
		},
		{
			name: "a sender known only by its LID",
			mut: func(_, info, _ map[string]any) {
				info["Sender"], info["Chat"], info["SenderAlt"] = "1234@lid", "1234@lid", ""
			},
			want: domain.ErrIgnored,
		},
		{
			name: "a receipt",
			mut:  func(ev, _, _ map[string]any) { ev["event"] = "Receipt" },
			want: domain.ErrIgnored,
		},
		{
			name: "a message the hub sent (SendMessage)",
			mut:  func(ev, _, _ map[string]any) { ev["event"] = "SendMessage" },
			want: domain.ErrIgnored,
		},
		{
			name: "a message with no text (an image)",
			mut: func(_, _, message map[string]any) {
				delete(message, "extendedTextMessage")
				message["imageMessage"] = map[string]any{"mimetype": "image/jpeg"}
			},
			want: domain.ErrIgnored,
		},
		{
			name: "something that is not JSON",
			raw:  `<html>502 Bad Gateway</html>`,
			want: domain.ErrIgnored,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.raw
			if body == "" {
				body = messageEvent(tc.mut)
			}
			if _, err := n.ParseInbound(inboundRequest(body)); !errors.Is(err, tc.want) {
				t.Fatalf("ParseInbound = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestAttributionIsTheLicenceNotice: Evolution Go's licence requires a
// visible notice that it is in use, so the adapter must carry one.
func TestAttributionIsTheLicenceNotice(t *testing.T) {
	n := newNotifier(t, http.NotFoundHandler())
	if !strings.Contains(n.Attribution(), "Evolution Go") {
		t.Errorf("Attribution() = %q, want it to name Evolution Go", n.Attribution())
	}
	if n.Name() != "evolution" {
		t.Errorf("Name() = %q", n.Name())
	}
}

// TestSend_HonoursTheCallersDeadline: an alert is not worth holding the
// hub's goroutine on a provider that went quiet.
func TestSend_HonoursTheCallersDeadline(t *testing.T) {
	release := make(chan struct{})
	// Released here rather than in a cleanup: the server's own cleanup
	// waits for its handlers, and cleanups run in reverse order.
	defer close(release)
	n := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := n.Send(ctx, "+221771234567", domain.OutboundMessage{Text: "x"}); err == nil {
		t.Fatal("a provider that never answers was reported as a success")
	}
}
