// Package handler exposes the relay module over HTTP: /relay/v1 for the
// relays, and the household's router routes for the app.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"

	agentshandler "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Audit actions: who asked for what, and what came back.
const (
	ActionCommandQueued = auditdomain.AuditAction("ROUTER_COMMAND_QUEUED")
	ActionCommandDone   = auditdomain.AuditAction("ROUTER_COMMAND_DONE")
)

// snapshotDevices is the snapshot kind the device list is stored under.
const snapshotDevices = "devices"

// Handler serves the relay endpoints.
type Handler struct {
	Guard     *tenantaccess.Guard
	Tokens    *agentshandler.TokenResolver
	Heartbeat usecase.Heartbeat
	Queue     usecase.Queue
	Snapshots usecase.Snapshots
	Enforcer  usecase.AutoRejectEnforcer
	Status    usecase.RelayStatus
	Policy    *Policy
	Audit     audit.EventLogger
	Now       func() time.Time
	// CallerWait is how long deviceAction waits for the relay's answer
	// before answering 202 with the command id.
	CallerWait time.Duration
	// LongPoll caps how long a relay may hold the command request open.
	LongPoll time.Duration
}

// RegisterRoutes mounts the household routes (kernel JWT required).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/tenants/{tenantId}/router/"
	mux.HandleFunc("GET "+p+"devices", h.devices)
	mux.HandleFunc("POST "+p+"devices/{mac}/block", h.block)
	mux.HandleFunc("POST "+p+"devices/{mac}/unblock", h.unblock)
	mux.HandleFunc("POST "+p+"commands", h.genericCommand)
	mux.HandleFunc("GET "+p+"commands/{commandId}", h.commandStatus)
	mux.HandleFunc("GET "+p+"auto-reject", h.autoRejectEnforcer)
	mux.HandleFunc("GET "+p+"relay", h.relayStatus)
}

// RegisterRelayRoutes mounts the routes a relay calls with its token.
func (h *Handler) RegisterRelayRoutes(mux *http.ServeMux) {
	m := h.Tokens.Middleware
	mux.Handle("POST /relay/v1/heartbeat", m(http.HandlerFunc(h.heartbeat)))
	mux.Handle("GET /relay/v1/commands", m(http.HandlerFunc(h.nextCommand)))
	mux.Handle("POST /relay/v1/commands/{commandId}/result", m(http.HandlerFunc(h.commandResult)))
	mux.Handle("POST /relay/v1/snapshots", m(http.HandlerFunc(h.pushSnapshot)))
}

// --- relay routes ---------------------------------------------------------

type heartbeatRequest struct {
	WiFi struct {
		SSID  string `json:"ssid"`
		BSSID string `json:"bssid"`
	} `json:"wifi"`
	RouterIdentity struct {
		Pin string `json:"pin"`
	} `json:"routerIdentity"`
	LANIP      string `json:"lanIp"`
	Version    string `json:"version"`
	Released   bool   `json:"released"`
	AutoReject bool   `json:"autoReject"`
}

type heartbeatResponse struct {
	Lease             string `json:"lease"`
	Epoch             int64  `json:"epoch"`
	TTLSeconds        int    `json:"ttlSeconds"`
	RetryAfterSeconds int    `json:"retryAfterSeconds"`
	Reason            string `json:"reason,omitempty"`
	BundleVersion     uint64 `json:"bundleVersion"`
	CredentialVersion uint64 `json:"credentialVersion"`
	PolicyVersion     uint64 `json:"policyVersion"`
}

func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	agent := agentshandler.AgentFrom(r.Context())
	out, err := h.Heartbeat.Execute(r.Context(), agent, domain.Report{
		SSID: req.WiFi.SSID, BSSID: req.WiFi.BSSID, RouterPin: req.RouterIdentity.Pin,
		LANIP: req.LANIP, Version: req.Version, Released: req.Released, AutoReject: req.AutoReject,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, heartbeatResponse{
		Lease: string(out.State), Epoch: out.Epoch,
		TTLSeconds: int(out.TTL.Seconds()), RetryAfterSeconds: int(out.RetryAfter.Seconds()),
		Reason: out.Reason, BundleVersion: out.BundleVersion, CredentialVersion: out.CredentialVersion,
		PolicyVersion: out.PolicyVersion,
	})
}

type commandResponse struct {
	ID       string          `json:"id"`
	Epoch    int64           `json:"epoch"`
	Envelope json.RawMessage `json:"envelope"`
	Body     []byte          `json:"body,omitempty"`
}

// nextCommand is the relay's long poll. It holds the request open rather
// than having phones poll every second: it survives a network change and
// needs nothing but the standard library (RFC-005).
func (h *Handler) nextCommand(w http.ResponseWriter, r *http.Request) {
	agent := agentshandler.AgentFrom(r.Context())
	wait := h.LongPoll
	if d, err := time.ParseDuration(r.URL.Query().Get("wait")); err == nil && d > 0 && d <= wait {
		wait = d
	}
	cmd, err := h.Queue.NextFor(r.Context(), agent, wait)
	switch {
	case errors.Is(err, domain.ErrNotLeaseHolder):
		// Not a failure: the relay lost the lease while polling, and must
		// go back to heartbeating.
		response.JSON(w, http.StatusConflict, map[string]string{"lease": string(domain.StateStandby)})
		return
	case err != nil:
		response.Error(w, mapErr(err))
		return
	case cmd == nil:
		response.JSON(w, http.StatusNoContent, nil)
		return
	}
	response.JSON(w, http.StatusOK, commandResponse{
		ID: cmd.ID, Epoch: cmd.Epoch, Envelope: cmd.Envelope, Body: cmd.Body,
	})
}

type resultRequest struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
}

func (h *Handler) commandResult(w http.ResponseWriter, r *http.Request) {
	var req resultRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	agent := agentshandler.AgentFrom(r.Context())
	cmdID := r.PathValue("commandId")
	signed := trust.SignedResult{Payload: req.Payload, Sig: req.Sig}

	// The hub checks the relay's signature as well as the app will: a
	// result it cannot attribute is not worth storing.
	res, err := h.Policy.VerifyResult(r.Context(), agent, signed, cmdID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if err := h.Queue.Complete(r.Context(), agent, cmdID, signed); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	// The actor is a relay, not a user, and audit_logs.actor_id references
	// users: it stays empty (NULL) and the relay is named in the metadata.
	h.Audit.Execute(r.Context(), audit.LogEventParams{
		TenantID: agent.TenantID, Action: ActionCommandDone,
		TargetType: "router_command",
		Metadata:   map[string]any{"status": res.Status, "agent_id": agent.ID, "command_id": cmdID},
	})
	response.JSON(w, http.StatusNoContent, nil)
}

func (h *Handler) pushSnapshot(w http.ResponseWriter, r *http.Request) {
	var req resultRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	agent := agentshandler.AgentFrom(r.Context())
	signed := trust.SignedSnapshot{Payload: req.Payload, Sig: req.Sig}
	if err := h.Policy.VerifySnapshot(r.Context(), agent, signed); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if err := h.Snapshots.Push(r.Context(), agent, signed); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusNoContent, nil)
}

// --- household routes -----------------------------------------------------

type devicesResponse struct {
	Snapshot json.RawMessage `json:"snapshot"`
	AgentID  string          `json:"agentId"`
	AsOf     time.Time       `json:"asOf"`
}

// devices serves the last state a relay pushed, with its age. The app shows
// the age: "as of two minutes ago" is honest, a silent stale list is not.
func (h *Handler) devices(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	snap, err := h.Snapshots.Latest(r.Context(), caller.TenantID, snapshotDevices)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, devicesResponse{Snapshot: snap.Payload, AgentID: snap.AgentID, AsOf: snap.AsOf})
}

type commandRequest struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
}

type submitResponse struct {
	CommandID string          `json:"commandId"`
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
}

func (h *Handler) block(w http.ResponseWriter, r *http.Request)   { h.deviceAction(w, r, "block") }
func (h *Handler) unblock(w http.ResponseWriter, r *http.Request) { h.deviceAction(w, r, "unblock") }

// deviceAction queues a command the owner's app signed and waits briefly
// for the relay's answer.
//
// The hub checks the caller's role and the signing key's delegation even
// though the relay checks both again. That is defence in depth, not the
// decision: the relay's check is the one that counts, because it does not
// trust this hub (RFC-005).
func (h *Handler) deviceAction(w http.ResponseWriter, r *http.Request, action string) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req commandRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	mac := r.PathValue("mac")
	signed := trust.SignedCommand{Payload: req.Payload, Sig: req.Sig}
	if err := h.Policy.CheckCommand(r.Context(), caller, signed); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	cmdID, err := h.Queue.Submit(r.Context(), usecase.SubmitParams{
		TenantID: caller.TenantID, IssuerUserID: caller.UserID, Signed: signed,
		WantMethod: http.MethodPost, WantPath: "/api/devices/" + mac + "/" + action,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	// target_id is a uuid column, and a device is identified by its MAC, so
	// the MAC travels in the metadata — which is also where the owner reads
	// which device was blocked, and by which command.
	h.Audit.Execute(r.Context(), audit.LogEventParams{
		TenantID: caller.TenantID, ActorID: caller.UserID, Action: ActionCommandQueued,
		TargetType: "device",
		Metadata:   map[string]any{"action": action, "mac": mac, "command_id": cmdID},
	})

	cmd, err := h.Queue.AwaitResult(r.Context(), caller.TenantID, cmdID, h.CallerWait)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if cmd.Status != domain.StatusDone {
		// Nobody answered in time. The command id is the receipt: the app
		// polls it rather than holding a request open from a phone.
		response.JSON(w, http.StatusAccepted, submitResponse{CommandID: cmdID, Status: string(cmd.Status)})
		return
	}
	response.JSON(w, http.StatusOK, submitResponse{CommandID: cmdID, Status: string(cmd.Status), Result: cmd.Result})
}

// genericRequest is POST router/commands: a command the app signed for any
// route of the relay's allow-list, with the body it signed (#50).
type genericRequest struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Body    []byte `json:"body,omitempty"`
}

// carried is what the hub agrees to carry on POST router/commands: the
// relay's allow-list (internal/agent), minus block / unblock, which keep
// their own routes. The relay decides again; this only keeps the hub from
// queueing what no relay would run.
var carried = []struct{ method, prefix, suffix string }{
	{http.MethodGet, "/api/devices", ""},
	{http.MethodGet, "/api/wifi/ssids", ""},
	{http.MethodGet, "/api/wifi/blocked", ""},
	{http.MethodGet, "/api/usage", ""},
	{http.MethodPost, "/api/devices/", "/wifi-block"},
	{http.MethodPost, "/api/devices/", "/wifi-unblock"},
	{http.MethodPatch, "/api/wifi/ssids/", ""},
	{http.MethodPost, "/api/internet/cut", ""},
	{http.MethodPost, "/api/internet/restore", ""},
}

// mayCarry matches a signed command's method and path against carried.
func mayCarry(method, path string) bool {
	if strings.ContainsAny(path, "?#") || strings.Contains(path, "..") {
		return false
	}
	for _, c := range carried {
		if c.method != method {
			continue
		}
		if c.suffix == "" && !strings.HasSuffix(c.prefix, "/") {
			if path == c.prefix {
				return true
			}
			continue
		}
		rest, ok := strings.CutPrefix(path, c.prefix)
		if !ok {
			continue
		}
		seg, ok := strings.CutSuffix(rest, c.suffix)
		if ok && seg != "" && !strings.Contains(seg, "/") {
			return true
		}
	}
	return false
}

// genericCommand queues a signed command for a route of the relay's
// allow-list other than block / unblock: consumption, SSIDs, Wi-Fi access,
// cut / restore Internet (#50). Same checks and same answer as deviceAction.
func (h *Handler) genericCommand(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	var req genericRequest
	if err := decode(r, &req); err != nil {
		response.Error(w, err)
		return
	}
	signed := trust.SignedCommand{Payload: req.Payload, Sig: req.Sig}
	cmd, err := signed.Unverified()
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if !mayCarry(cmd.Method, cmd.Path) {
		response.Error(w, kernelerrors.Forbidden("the relay does not run this route"))
		return
	}
	if err := h.Policy.CheckCommand(r.Context(), caller, signed); err != nil {
		response.Error(w, mapErr(err))
		return
	}
	cmdID, err := h.Queue.Submit(r.Context(), usecase.SubmitParams{
		TenantID: caller.TenantID, IssuerUserID: caller.UserID, Signed: signed, Body: req.Body,
		WantMethod: cmd.Method, WantPath: cmd.Path,
	})
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if cmd.Method != http.MethodGet {
		h.Audit.Execute(r.Context(), audit.LogEventParams{
			TenantID: caller.TenantID, ActorID: caller.UserID, Action: ActionCommandQueued,
			TargetType: "router",
			Metadata:   map[string]any{"method": cmd.Method, "path": cmd.Path, "command_id": cmdID},
		})
	}
	done, err := h.Queue.AwaitResult(r.Context(), caller.TenantID, cmdID, h.CallerWait)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	if done.Status != domain.StatusDone {
		response.JSON(w, http.StatusAccepted, submitResponse{CommandID: cmdID, Status: string(done.Status)})
		return
	}
	response.JSON(w, http.StatusOK, submitResponse{CommandID: cmdID, Status: string(done.Status), Result: done.Result})
}

func (h *Handler) commandStatus(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	cmd, err := h.Queue.Status(r.Context(), caller.TenantID, r.PathValue("commandId"))
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	response.JSON(w, http.StatusOK, submitResponse{CommandID: cmd.ID, Status: string(cmd.Status), Result: cmd.Result})
}

type enforcerResponse struct {
	// Enforcer is "relay" while the household's relay enforces auto-reject,
	// "phone" otherwise.
	Enforcer string `json:"enforcer"`
	AgentID  string `json:"agentId,omitempty"`
	// RelayHeartbeatAt and RelayHeartbeatAgeSeconds date the relay's last
	// heartbeat. The phone goes by the age, on its own clock: the two clocks
	// need not agree.
	RelayHeartbeatAt         *time.Time `json:"relayHeartbeatAt,omitempty"`
	RelayHeartbeatAgeSeconds *int       `json:"relayHeartbeatAgeSeconds,omitempty"`
}

// autoRejectEnforcer tells a phone whether the household's relay enforces
// auto-reject, so its watch service stands down (RFC-006, #36).
func (h *Handler) autoRejectEnforcer(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	e, err := h.Enforcer.Execute(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := enforcerResponse{Enforcer: "phone"}
	if e.Relay {
		at, age := e.LastHeartbeat.UTC(), int(e.Age.Seconds())
		out = enforcerResponse{Enforcer: "relay", AgentID: e.AgentID, RelayHeartbeatAt: &at, RelayHeartbeatAgeSeconds: &age}
	}
	response.JSON(w, http.StatusOK, out)
}

type relayStatusResponse struct {
	// State is active, standby, offline or none.
	State   string `json:"state"`
	AgentID string `json:"agentId,omitempty"`
	// LastSeenAt and AgeSeconds date the heartbeat the state rests on. The
	// app words "offline since" from the age, on its own clock.
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
	AgeSeconds *int       `json:"ageSeconds,omitempty"`
	// Reason is why a heard relay cannot reach the box.
	Reason string `json:"reason,omitempty"`
}

// relayStatus tells the app whether the household's relay is on the box
// (#46), so a remote screen can say "relay offline since 14:02" instead of
// waiting for a command to expire.
func (h *Handler) relayStatus(w http.ResponseWriter, r *http.Request) {
	caller, err := h.Guard.Require(r)
	if err != nil {
		response.Error(w, err)
		return
	}
	st, err := h.Status.Execute(r.Context(), caller.TenantID)
	if err != nil {
		response.Error(w, mapErr(err))
		return
	}
	out := relayStatusResponse{State: string(st.State), AgentID: st.AgentID, Reason: st.Reason}
	if !st.LastSeen.IsZero() {
		at, age := st.LastSeen.UTC(), int(st.Age.Seconds())
		out.LastSeenAt, out.AgeSeconds = &at, &age
	}
	response.JSON(w, http.StatusOK, out)
}

// --- plumbing -------------------------------------------------------------

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return kernelerrors.BadRequest("malformed request body")
	}
	return nil
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNoCommand):
		return kernelerrors.NotFound("no such command")
	case errors.Is(err, domain.ErrNoSnapshot):
		return kernelerrors.NotFound("no relay has reported this household's devices yet")
	case errors.Is(err, domain.ErrNotLeaseHolder):
		return kernelerrors.Conflict("this relay does not hold the lease")
	case errors.Is(err, domain.ErrStaleEpoch):
		return kernelerrors.Conflict("the lease changed hands, this result is stale")
	case errors.Is(err, trust.ErrExpired), errors.Is(err, trust.ErrTenant),
		errors.Is(err, trust.ErrBodyMismatch), errors.Is(err, trust.ErrMalformed):
		return kernelerrors.BadRequest(err.Error())
	// A command with no signature is not a malformed request but a refused
	// one: the hub answers it like any other command it will not carry.
	case errors.Is(err, trust.ErrUnsigned), errors.Is(err, trust.ErrScope), errors.Is(err, trust.ErrOwnerOnly),
		errors.Is(err, trust.ErrRevoked), errors.Is(err, trust.ErrUnknownKey), errors.Is(err, trust.ErrBadSignature),
		errors.Is(err, trust.ErrDelegationExpired):
		return kernelerrors.Forbidden(err.Error())
	default:
		return kernelerrors.WrapInternal(err)
	}
}
