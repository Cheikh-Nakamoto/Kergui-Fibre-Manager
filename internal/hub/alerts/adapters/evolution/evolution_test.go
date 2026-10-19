package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

const (
	instance = "kergui-pilote"
	apiKey   = "instance-key"
)

func newNotifier(t *testing.T, h http.Handler) (*Notifier, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	n, err := New(Config{BaseURL: srv.URL, Instance: instance, APIKey: apiKey})
	if err != nil {
		t.Fatal(err)
	}
	return n, srv
}

func TestNew_RefusesHalfConfigured(t *testing.T) {
	for _, cfg := range []Config{
		{BaseURL: "", Instance: instance, APIKey: apiKey},
		{BaseURL: "not a url", Instance: instance, APIKey: apiKey},
		{BaseURL: "http://evolution:8080", Instance: "", APIKey: apiKey},
		{BaseURL: "http://evolution:8080", Instance: instance, APIKey: ""},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) was accepted", cfg)
		}
	}
}

func TestSend(t *testing.T) {
	var got struct {
		path   string
		apikey string
		body   sendRequest
	}
	n, _ := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.apikey = r.URL.Path, r.Header.Get(APIKeyHeader)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		_, _ = w.Write([]byte(`{"key":{"id":"3EB0C1"}}`))
	}))

	id, err := n.Send(context.Background(), "+221771234567",
		domain.OutboundMessage{Text: "Kergui : un nouvel appareil…", AlertID: "alert-1"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "3EB0C1" {
		t.Errorf("message id = %q, want the provider's own", id)
	}
	if got.path != DefaultSendPath {
		t.Errorf("path = %q, want %q", got.path, DefaultSendPath)
	}
	if got.apikey != apiKey {
		t.Error("the instance key did not travel in the apikey header")
	}
	// WhatsApp wants the digits, not the plus.
	if got.body.Number != "221771234567" {
		t.Errorf("number = %q, want it without the leading plus", got.body.Number)
	}
	if !strings.HasPrefix(got.body.Text, "Kergui") || got.body.Instance != instance {
		t.Errorf("body = %+v", got.body)
	}
}

// TestSend_NotActivated: a fresh instance answers 503 until it is
// registered in its Manager. That is a licensing state, not a hiccup, so
// the hub reports it as such and an administrator can see it.
func TestSend_NotActivated(t *testing.T) {
	n, _ := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"instance not activated"}`))
	}))
	if _, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"}); !errors.Is(err, domain.ErrNotActivated) {
		t.Fatalf("Send error = %v, want ErrNotActivated", err)
	}
	if err := n.Status(context.Background()); !errors.Is(err, domain.ErrNotActivated) {
		t.Fatalf("Status error = %v, want ErrNotActivated", err)
	}
}

// TestSend_RefusedKeyDoesNotLeakIt: an error reaches logs and APIs, so it
// must say nothing about the key itself.
func TestSend_RefusedKeyDoesNotLeakIt(t *testing.T) {
	n, _ := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	_, err := n.Send(context.Background(), "+221771234567", domain.OutboundMessage{Text: "x"})
	if err == nil {
		t.Fatal("a refused key was reported as a success")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("the error carries the API key: %v", err)
	}
}

func TestStatus_OK(t *testing.T) {
	n, _ := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DefaultStatusPath {
			t.Errorf("status path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"instance":{"state":"open"}}`))
	}))
	if err := n.Status(context.Background()); err != nil {
		t.Fatalf("Status: %v", err)
	}
}

// inboundRequest builds a webhook call carrying one event.
func inboundRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodPost, "/whatsapp/v1/inbound/secret", strings.NewReader(body))
}

func TestParseInbound(t *testing.T) {
	n, _ := newNotifier(t, http.NotFoundHandler())

	msg, err := n.ParseInbound(inboundRequest(t, `{
	  "event": "messages.upsert",
	  "instance": "kergui-pilote",
	  "data": {
	    "key": {"remoteJid": "221771234567@s.whatsapp.net", "fromMe": false, "id": "3EB0C2"},
	    "messageType": "extendedTextMessage",
	    "message": {"extendedTextMessage": {"text": "1", "contextInfo": {"stanzaId": "3EB0C1"}}},
	    "messageTimestamp": 1793000000
	  }
	}`))
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}
	if msg.From != "+221771234567" || msg.Text != "1" {
		t.Errorf("message = %+v", msg)
	}
	if msg.QuotedProviderID != "3EB0C1" {
		t.Errorf("the quoted message id was lost: %+v", msg)
	}
	if msg.ProviderID != "3EB0C2" || msg.At.Unix() != 1793000000 {
		t.Errorf("message = %+v", msg)
	}

	// A plain conversation message carries its text elsewhere.
	msg, err = n.ParseInbound(inboundRequest(t, `{
	  "instance": "kergui-pilote",
	  "data": {"key": {"remoteJid": "221771234567:12@s.whatsapp.net"}, "message": {"conversation": "STOP"}}
	}`))
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}
	if msg.Text != "STOP" || msg.From != "+221771234567" {
		t.Errorf("message = %+v, want STOP from the number without its device suffix", msg)
	}
}

func TestParseInbound_Refusals(t *testing.T) {
	n, _ := newNotifier(t, http.NotFoundHandler())
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{
			name: "another instance's traffic",
			body: `{"instance":"autre","data":{"key":{"remoteJid":"221771234567@s.whatsapp.net"},"message":{"conversation":"1"}}}`,
			want: domain.ErrNotAuthentic,
		},
		{
			name: "a message we sent ourselves",
			body: `{"instance":"kergui-pilote","data":{"key":{"remoteJid":"221771234567@s.whatsapp.net","fromMe":true},"message":{"conversation":"1"}}}`,
			want: domain.ErrIgnored,
		},
		{
			name: "a group chat",
			body: `{"instance":"kergui-pilote","data":{"key":{"remoteJid":"2217712345-1600000000@g.us"},"message":{"conversation":"1"}}}`,
			want: domain.ErrIgnored,
		},
		{
			name: "an event with no text (a receipt)",
			body: `{"instance":"kergui-pilote","event":"messages.update","data":{"key":{"remoteJid":"221771234567@s.whatsapp.net"}}}`,
			want: domain.ErrIgnored,
		},
		{
			name: "something that is not JSON",
			body: `<html>502 Bad Gateway</html>`,
			want: domain.ErrIgnored,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := n.ParseInbound(inboundRequest(t, tc.body)); !errors.Is(err, tc.want) {
				t.Fatalf("ParseInbound = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestAttributionIsTheLicenceNotice: Evolution Go's licence requires a
// visible notice that it is in use, so the adapter must carry one.
func TestAttributionIsTheLicenceNotice(t *testing.T) {
	n, _ := newNotifier(t, http.NotFoundHandler())
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
	n, _ := newNotifier(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := n.Send(ctx, "+221771234567", domain.OutboundMessage{Text: "x"}); err == nil {
		t.Fatal("a provider that never answers was reported as a success")
	}
}
