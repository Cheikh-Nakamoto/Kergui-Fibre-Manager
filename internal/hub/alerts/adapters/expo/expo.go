// Package expo is the app push adapter (issue #49): it hands a notification
// to Expo's push service, which delivers it over FCM on Android and APNs on
// iOS to an app that is closed.
//
// The app registers an Expo push token (expo-notifications'
// getExpoPushTokenAsync), which the hub stores per household and member.
// Expo needs the FCM credentials of the app's Firebase project uploaded to
// the EAS project once (an administrator's step, RFC-005, issue 49); the hub
// itself holds at most an Expo access token, when "enhanced security for
// push notifications" is turned on for the project.
//
// Only the ticket of the send is read: a "DeviceNotRegistered" there means
// the installation is gone and the hub forgets the token. Receipts (the
// second, delayed answer) are not polled — an alert that FCM could not
// deliver is not one the hub could have done anything else about, and
// WhatsApp carries the same alert.
package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// DefaultBaseURL is Expo's push service.
const DefaultBaseURL = "https://exp.host"

// sendPath is the send route under the base URL.
const sendPath = "/--/api/v2/push/send"

// ChannelID is the Android notification channel the app creates for alerts
// (mobile/src/alerts/alerts.ts, CHANNEL): high importance, so the alert
// shows as a heads-up with the app closed.
const ChannelID = "new-devices"

// maxAnswerBytes bounds Expo's answer; a ticket is a line of JSON.
const maxAnswerBytes = 64 << 10

// Config points at Expo's push service.
type Config struct {
	// BaseURL defaults to DefaultBaseURL; tests point it at a fake.
	BaseURL string
	// AccessToken is the EAS project's push access token, when the project
	// requires one. Empty sends without.
	AccessToken string
	Client      *http.Client
}

// Pusher is the Expo push adapter.
type Pusher struct {
	base   string
	token  string
	client *http.Client
}

var _ domain.Pusher = (*Pusher)(nil)

// New builds the adapter.
func New(cfg Config) *Pusher {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Pusher{base: base, token: cfg.AccessToken, client: client}
}

// Name identifies the channel.
func (p *Pusher) Name() string { return "expo" }

// message is one entry of Expo's send body.
type message struct {
	To        string            `json:"to"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Data      map[string]string `json:"data,omitempty"`
	Sound     string            `json:"sound"`
	Priority  string            `json:"priority"`
	ChannelID string            `json:"channelId"`
}

// answer is Expo's send answer: one ticket per message, or request errors.
type answer struct {
	Data []struct {
		Status  string `json:"status"`
		ID      string `json:"id"`
		Message string `json:"message"`
		Details struct {
			Error string `json:"error"`
		} `json:"details"`
	} `json:"data"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// Push sends one notification.
func (p *Pusher) Push(ctx context.Context, to domain.PushTarget, msg domain.PushMessage) error {
	body, err := json.Marshal([]message{{
		To: to.Token, Title: msg.Title, Body: msg.Body, Data: msg.Data,
		Sound: "default", Priority: "high", ChannelID: ChannelID,
	}})
	if err != nil {
		return fmt.Errorf("expo: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+sendPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("expo: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("expo: send: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	var a answer
	jsonErr := json.Unmarshal(raw, &a)
	if resp.StatusCode != http.StatusOK {
		if jsonErr == nil && len(a.Errors) > 0 {
			return fmt.Errorf("expo: HTTP %d: %s", resp.StatusCode, a.Errors[0].Code)
		}
		return fmt.Errorf("expo: HTTP %d", resp.StatusCode)
	}
	if jsonErr != nil {
		return fmt.Errorf("expo: unreadable answer: %w", jsonErr)
	}
	if len(a.Data) != 1 {
		return fmt.Errorf("expo: %d tickets for one message", len(a.Data))
	}
	ticket := a.Data[0]
	switch {
	case ticket.Status == "ok":
		return nil
	case ticket.Details.Error == "DeviceNotRegistered":
		return domain.ErrPushTokenGone
	default:
		// Expo's message quotes the token; only its error code is kept, so
		// a token never reaches a log or the deliveries table.
		code := ticket.Details.Error
		if code == "" {
			code = "unknown"
		}
		return fmt.Errorf("expo: refused: %s", code)
	}
}
