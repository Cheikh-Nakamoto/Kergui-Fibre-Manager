package expo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

var target = domain.PushTarget{TenantID: "t", UserID: "u", Platform: domain.PlatformAndroid, Token: "ExponentPushToken[abc]"}

var msg = domain.PushMessage{Title: "Nouvel appareil", Body: "TECNO vient de rejoindre votre Wi-Fi.", Data: map[string]string{"mac": "aa:11:22:33:44:55"}}

func server(t *testing.T, status int, answer string, seen *map[string]any, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/--/api/v2/push/send" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		var body []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 {
			t.Errorf("body: %v %v", body, err)
		} else if seen != nil {
			*seen = body[0]
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPush_SendsTheMessageToExpo(t *testing.T) {
	var seen map[string]any
	var auth string
	srv := server(t, 200, `{"data":[{"status":"ok","id":"r-1"}]}`, &seen, &auth)
	p := New(Config{BaseURL: srv.URL, AccessToken: "secret"})
	if p.Name() != "expo" {
		t.Errorf("Name = %q", p.Name())
	}
	if err := p.Push(context.Background(), target, msg); err != nil {
		t.Fatal(err)
	}
	if seen["to"] != target.Token || seen["title"] != msg.Title || seen["body"] != msg.Body {
		t.Errorf("sent %v", seen)
	}
	if d, _ := seen["data"].(map[string]any); d["mac"] != "aa:11:22:33:44:55" {
		t.Errorf("data = %v", seen["data"])
	}
	if seen["channelId"] != ChannelID || seen["priority"] != "high" {
		t.Errorf("Android channel / priority = %v / %v", seen["channelId"], seen["priority"])
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestPush_NoAccessTokenSendsNoAuthorization(t *testing.T) {
	auth := "unset"
	srv := server(t, 200, `{"data":[{"status":"ok","id":"r-1"}]}`, nil, &auth)
	if err := New(Config{BaseURL: srv.URL}).Push(context.Background(), target, msg); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("Authorization = %q, want none", auth)
	}
}

func TestPush_DeviceNotRegisteredIsAGoneToken(t *testing.T) {
	srv := server(t, 200, `{"data":[{"status":"error","message":"not a registered push token","details":{"error":"DeviceNotRegistered"}}]}`, nil, nil)
	err := New(Config{BaseURL: srv.URL}).Push(context.Background(), target, msg)
	if !errors.Is(err, domain.ErrPushTokenGone) {
		t.Fatalf("err = %v, want ErrPushTokenGone", err)
	}
}

func TestPush_OtherErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"ticket error":   {200, `{"data":[{"status":"error","message":"too big","details":{"error":"MessageTooBig"}}]}`},
		"request error":  {400, `{"errors":[{"code":"VALIDATION_ERROR","message":"bad"}]}`},
		"server down":    {503, `oops`},
		"not json":       {200, `oops`},
		"empty receipts": {200, `{"data":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := server(t, tc.status, tc.body, nil, nil)
			err := New(Config{BaseURL: srv.URL}).Push(context.Background(), target, msg)
			if err == nil || errors.Is(err, domain.ErrPushTokenGone) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestPush_TheTokenIsNotInTheError(t *testing.T) {
	srv := server(t, 200, `{"data":[{"status":"error","message":"\"ExponentPushToken[abc]\" is not valid","details":{"error":"InvalidCredentials"}}]}`, nil, nil)
	err := New(Config{BaseURL: srv.URL}).Push(context.Background(), target, msg)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "ExponentPushToken[abc]") {
		t.Errorf("the token leaks into the error: %v", err)
	}
}
