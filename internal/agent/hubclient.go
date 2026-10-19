package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Lease states as the hub spells them.
const (
	leaseActive  = "active"
	leaseStandby = "standby"
)

// maxHubBody caps what the relay will read from the hub. The hub is not
// trusted with the box; it is not trusted with the relay's memory either.
const maxHubBody = 4 << 20

// hubClient speaks the /relay/v1 protocol. Everything it returns is data to
// be verified, never an instruction to be followed.
type hubClient struct {
	base    string
	token   string
	http    *http.Client
	timings Timings
}

func newHubClient(base, token string, c *http.Client, t Timings) *hubClient {
	if c == nil {
		// The long poll holds a request open, so the client itself must
		// not time out; each call carries its own context deadline.
		c = &http.Client{}
	}
	return &hubClient{base: strings.TrimRight(base, "/"), token: token, http: c, timings: t}
}

type heartbeatRequest struct {
	WiFi struct {
		SSID  string `json:"ssid"`
		BSSID string `json:"bssid"`
	} `json:"wifi"`
	RouterIdentity struct {
		Pin string `json:"pin"`
	} `json:"routerIdentity"`
	LANIP    string `json:"lanIp"`
	Version  string `json:"version"`
	Released bool   `json:"released"`
}

type heartbeatResponse struct {
	Lease             string `json:"lease"`
	Epoch             int64  `json:"epoch"`
	TTLSeconds        int    `json:"ttlSeconds"`
	RetryAfterSeconds int    `json:"retryAfterSeconds"`
	Reason            string `json:"reason"`
	BundleVersion     uint64 `json:"bundleVersion"`
	CredentialVersion uint64 `json:"credentialVersion"`
}

func (h *hubClient) heartbeat(ctx context.Context, req heartbeatRequest) (heartbeatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timings.HubTimeout)
	defer cancel()
	var out heartbeatResponse
	err := h.call(ctx, http.MethodPost, "/relay/v1/heartbeat", req, &out)
	return out, err
}

func (h *hubClient) bundle(ctx context.Context) (trust.Signed[trust.Bundle], error) {
	ctx, cancel := context.WithTimeout(ctx, h.timings.HubTimeout)
	defer cancel()
	var out struct {
		Payload []byte `json:"payload"`
		Sig     []byte `json:"sig"`
	}
	if err := h.call(ctx, http.MethodGet, "/relay/v1/trust", nil, &out); err != nil {
		return trust.Signed[trust.Bundle]{}, err
	}
	return trust.Signed[trust.Bundle]{Payload: out.Payload, Sig: out.Sig}, nil
}

func (h *hubClient) credentials(ctx context.Context) ([]byte, uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timings.HubTimeout)
	defer cancel()
	var out struct {
		Blob    []byte `json:"blob"`
		Version uint64 `json:"version"`
	}
	if err := h.call(ctx, http.MethodGet, "/relay/v1/credentials", nil, &out); err != nil {
		return nil, 0, err
	}
	return out.Blob, out.Version, nil
}

// queuedCommand is one command as the hub hands it over.
type queuedCommand struct {
	ID       string              `json:"id"`
	Epoch    int64               `json:"epoch"`
	Envelope trust.SignedCommand `json:"envelope"`
	Body     []byte              `json:"body"`
}

// errLeaseLost says the hub no longer considers this relay the holder.
var errLeaseLost = fmt.Errorf("agent: the lease is no longer ours")

// next long-polls for the next command. It returns (nil, nil) when the wait
// ran out with nothing to do.
func (h *hubClient) next(ctx context.Context, wait time.Duration) (*queuedCommand, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+h.timings.HubTimeout)
	defer cancel()
	req, err := h.request(ctx, http.MethodGet, fmt.Sprintf("/relay/v1/commands?wait=%s", wait), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: long poll: %w", err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusConflict:
		return nil, errLeaseLost
	case http.StatusOK:
		var out queuedCommand
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxHubBody)).Decode(&out); err != nil {
			return nil, fmt.Errorf("agent: malformed command from the hub: %w", err)
		}
		return &out, nil
	default:
		return nil, statusError(resp)
	}
}

func (h *hubClient) postResult(ctx context.Context, cmdID string, res trust.SignedResult) error {
	ctx, cancel := context.WithTimeout(ctx, h.timings.HubTimeout)
	defer cancel()
	return h.call(ctx, http.MethodPost, "/relay/v1/commands/"+cmdID+"/result",
		map[string]any{"payload": res.Payload, "sig": res.Sig}, nil)
}

func (h *hubClient) postSnapshot(ctx context.Context, snap trust.SignedSnapshot) error {
	ctx, cancel := context.WithTimeout(ctx, h.timings.HubTimeout)
	defer cancel()
	return h.call(ctx, http.MethodPost, "/relay/v1/snapshots",
		map[string]any{"payload": snap.Payload, "sig": snap.Sig}, nil)
}

// call sends one JSON request and decodes the answer when out is not nil.
func (h *hubClient) call(ctx context.Context, method, path string, body, out any) error {
	req, err := h.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent: %s %s: %w", method, path, err)
	}
	defer drain(resp)
	if resp.StatusCode >= 300 {
		return statusError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxHubBody)).Decode(out); err != nil {
		return fmt.Errorf("agent: malformed answer from the hub: %w", err)
	}
	return nil
}

func (h *hubClient) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("agent: encode request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("agent: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	return req, nil
}

// statusError turns a hub error into one the relay can log. The hub's
// message is data, so it is truncated and never acted upon.
func statusError(resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("agent: hub answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxHubBody))
	_ = resp.Body.Close()
}
