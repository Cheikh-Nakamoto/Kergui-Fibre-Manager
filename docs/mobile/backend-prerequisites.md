# Mobile app — backend prerequisites (Milestone 4.0)

> **v2 (hub).** This document describes the optional server-backed version.
> The v1 app is stand-alone and needs none of it: see
> [RFC-004](../rfcs/RFC-004-standalone-mobile-app.md).

Part of [Phase 4](README.md). Everything the Go server needs before the first app
build talks to it. It follows the existing layering
([architecture/overview.md](../architecture/overview.md)): domain → use cases +
ports → adapters → composition root. Rules come from [security.md](security.md).

## 1. Domain

`internal/domain/appclient.go`

```go
type AppRole string

const (
	RoleAdmin  AppRole = "admin"
	RoleMember AppRole = "member"
)

// AppClient is a paired phone. The token itself is never stored.
type AppClient struct {
	ID         string
	RouterID   string
	Role       AppRole
	MemberID   string // RoleMember only
	Name       string // "Pixel 8 de Cheikh"
	Platform   string // "android"
	TokenHash  [32]byte
	PushToken  string // Expo push token, "" if none
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  *time.Time
}

// PairingCode is a one-time code; only its hash is stored.
type PairingCode struct {
	CodeHash  [32]byte
	Role      AppRole
	MemberID  string
	ExpiresAt time.Time
	UsedAt    *time.Time
}
```

Validation (`Validate()`), like `Member.Validate`: a member role needs a
`MemberID`, an admin role must not have one, names trimmed and bounded.
New errors in `internal/domain/errors.go`: `ErrUnauthorized`, `ErrForbidden`,
`ErrCodeInvalid`.

## 2. Ports and use cases

`internal/usecase/port/ports.go` — next to `MemberRepository`:

```go
type AppClientRepository interface {
	SaveCode(ctx context.Context, c domain.PairingCode) error
	ConsumeCode(ctx context.Context, hash [32]byte, now time.Time) (domain.PairingCode, error) // atomic
	Save(ctx context.Context, c domain.AppClient) error
	ByTokenHash(ctx context.Context, hash [32]byte) (domain.AppClient, error)
	List(ctx context.Context, routerID string) ([]domain.AppClient, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Revoke(ctx context.Context, id string, at time.Time) error
	RevokeMember(ctx context.Context, memberID string, at time.Time) error
	SetPushToken(ctx context.Context, id, token string) error
	PushTokens(ctx context.Context, role domain.AppRole, memberID string) ([]string, error)
}

type Notifier interface {
	Notify(ctx context.Context, to []string, n Notification) error
}
```

`Notification{Title, Body, Link string; Data map[string]string}` lives in
`port/dto.go`.

Use cases:

| File | Interactor | Methods |
|---|---|---|
| `internal/usecase/pairing.go` | `Pairing` | `IssueCode(role, memberID) (code, url, exp)`, `Redeem(code, name, platform) (token, AppClient)`, `Authenticate(token) (AppClient)`, `List`, `Revoke`, `SetPushToken` |
| `internal/usecase/me.go` | `MemberSpace` | `Summary(memberID)` (amount due, status ok/reminded/cut, pending payment), `Usage(memberID, period)`, `Invoices(memberID)`, `Pay(memberID)` — composed from `Metering`, `Billing`, `Members`, `Captive` state; **never** returns another member's devices |
| `internal/usecase/notify.go` | `Notifications` | `DeviceSeen`, `PaymentPaid`, `ReminderDue`, `MemberCut`, `RouterDown` — resolve recipients through `AppClientRepository.PushTokens` and call `Notifier` |

`Members.Delete` calls `AppClientRepository.RevokeMember`.
Tests: same table-driven style as `members` / `billing` tests, with an in-memory
repository and a fake clock (`port.Clock`).

## 3. Persistence

`internal/adapter/persistence/sqlite/migrations/0008_app_clients.sql`

```sql
-- Paired phones (mobile app) and the one-time codes that pair them. Tokens and
-- codes are stored as SHA-256 hashes only. Times are Unix seconds, as in
-- 0004_members.sql; 0 means "never".

CREATE TABLE app_clients (
    id           TEXT PRIMARY KEY,
    router_id    TEXT NOT NULL,
    role         TEXT NOT NULL,          -- 'admin' | 'member'
    member_id    TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL,
    platform     TEXT NOT NULL DEFAULT 'android',
    token_hash   BLOB NOT NULL UNIQUE,
    push_token   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL DEFAULT 0,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX app_clients_by_member ON app_clients (member_id);

CREATE TABLE pairing_codes (
    code_hash  BLOB PRIMARY KEY,
    router_id  TEXT NOT NULL,
    role       TEXT NOT NULL,
    member_id  TEXT NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL,
    used_at    INTEGER NOT NULL DEFAULT 0
);
```

Repository in `appclient_repo.go`, same style as `member_repo.go`;
`ConsumeCode` is a single `UPDATE … SET used_at = ? WHERE code_hash = ? AND
used_at = 0 AND expires_at > ? RETURNING …`, so two phones cannot redeem the same
code. Expired codes are purged at startup.

## 4. HTTP adapter

### Versioning

Register every existing route under `/api/v1/…` **and** keep `/api/…` for the
dashboard, by mounting the same handlers twice in `Server.Handler()`
(`internal/adapter/controller/httpapi/server.go`). The tunnel publishes `/api/v1/`
only. `GET /api/v1/health` adds `"api_version": 1` and `"server_id"`.

### Middleware — `httpapi/auth.go`

`func (s *Server) authorize(next http.Handler) http.Handler`, inserted between
`logRequests` and the mux:

1. Non-API paths and `POST /api/v1/pair/redeem` pass through.
2. Trusted loopback (see [security.md](security.md#authorization)) passes as admin.
3. Otherwise parse `Authorization: Bearer`, call `Pairing.Authenticate`, put the
   `AppClient` in the request context, enforce scope and LAN-only routes.
4. Map errors: missing/unknown/revoked → `401`, wrong scope / tunnelled LAN-only
   route → `403`.

Logging: never log the header; log `client=<id> role=<role>`.

### New routes

| Method & path | Scope | Body → Response |
|---|---|---|
| `POST /api/v1/pair/redeem` | public | `{code, device_name, platform}` → `{token, client_id, role, member_id?, server_id, server_name}` |
| `POST /api/v1/pair/codes` | admin | `{role, member_id?}` → `{code, url, expires_at}` (role `admin` LAN-only) |
| `GET /api/v1/clients` | admin | → `AppClient[]` (no hashes, no push tokens) |
| `DELETE /api/v1/clients/{id}` | admin | → `204` |
| `PUT /api/v1/me/push-token` | any | `{token}` → `204` |
| `GET /api/v1/me` | member | → `{member, amount_due_xof, status, pending_payment?}` |
| `GET /api/v1/me/usage?period=YYYY-MM` | member | → per-device bytes of the member's own devices |
| `GET /api/v1/me/invoices` | member | → invoices + payments of this member |
| `POST /api/v1/me/payments` | member | → `{checkout_url, amount_xof, period}` (reuses the pending one) |
| `GET /pair` | public | small HTML page: "open in the app / get it on Google Play" (App Links fallback) |
| `GET /.well-known/assetlinks.json` | public | Android App Links for `https://app.<domain>/pair` |

Existing admin routes are unchanged in shape. `GET /api/v1/logs` and
`GET /api/v1/config/env` become LAN-only.

### Contract — `docs/openapi.yaml`

Hand-written OpenAPI 3.1 describing every route above and every existing one
(`DeviceView`, `WiFiSSID`, `RouterInfo`, `InspectReport`, members, payments,
invoices, billing settings). A Go test (`httpapi/openapi_test.go`) parses the
`mux.HandleFunc` patterns registered by `Handler()` and fails if one is missing
from the spec — the spec cannot drift silently. `docs/api.md` links to it.

## 5. TLS on the LAN

- New listener inside the captive stack: `captive.SNIProxy` gets a
  `Local map[string]net.Listener`-style hook; when the ClientHello SNI equals
  `KERGUI_APP_HOST`, the raw connection is passed to an in-process
  `tls.NewListener` serving `Server.Handler()`. Other names keep the relay to the
  payment sites (`DefaultPaymentHosts`), unchanged.
- Certificate: [certmagic](https://github.com/caddyserver/certmagic) (pure Go,
  keeps `CGO_ENABLED=0`) with the Cloudflare DNS-01 provider; storage in
  `/data/certs` (the container root filesystem is read-only, `./data` is the
  only writable volume).
- Captive DNS (`captive/dns.go`): answer `KERGUI_APP_HOST` with the LAN address for
  every client, captive or not.
- Without `--captive`, the LAN listener is not started: the app then works only
  through the tunnel (documented, acceptable).

## 6. Push adapter

`internal/adapter/notify/expopush/client.go` implements `port.Notifier` with a
`POST https://exp.host/--/api/v2/push/send` (batches of 100, receipts checked
later, `DeviceNotRegistered` → clear that push token). Optional access token
`KERGUI_EXPO_ACCESS_TOKEN`. A `nop` notifier is wired when unset. Same structure
and test style as `internal/adapter/payment/naboopay`.

Wiring (composition root, `internal/adapter/controller/cli/cli.go`):

- `OnChange` (already called after cuts/payments) and the webhook path call
  `Notifications.PaymentPaid` / `MemberCut`;
- the device listing marks `NewlySeen` → `Notifications.DeviceSeen` (once per MAC);
- the reminder job → `Notifications.ReminderDue`;
- a failed router read for N consecutive minutes → `Notifications.RouterDown`.

## 7. CLI and dashboard

- `kergui pair --role admin|member [--member <id>]` prints a terminal QR
  (e.g. `github.com/mdp/qrterminal`) and the URL.
- `kergui clients list|revoke <id>`.
- Dashboard, Réglages: "Téléphones appairés" (list, revoke, add phone → QR image
  generated server-side as PNG, no JS dependency); Membres: "Inviter dans l'app".
- `docs/cli.md`, `docs/api.md`, `docs/configuration.md` updated accordingly.

## 8. Configuration

Added to `internal/infra/config/config.go` and to the Réglages env list
(`EnvVar`), all plain (non-quoted) values in `.env`:

| Variable | Required | Purpose |
|---|---|---|
| `KERGUI_APP_HOST` | for the app | Public hostname of the app API, e.g. `app.cheikhmodiouf.org` |
| `KERGUI_CF_DNS_TOKEN` | for LAN TLS | Cloudflare API token, `Zone:DNS:Edit` on the zone only (secret) |
| `KERGUI_ACME_EMAIL` | for LAN TLS | Let's Encrypt account e-mail |
| `KERGUI_EXPO_ACCESS_TOKEN` | no | Expo push access token (secret) |
| `KERGUI_TRUST_LOOPBACK` | no | `true` (default) lets the local dashboard call the API without a token |

## 9. Definition of done (Milestone 4.0)

- `make check` green, `tests/architecture_test.go` still green (no adapter import
  in use cases).
- Through the tunnel: `curl https://app.<domain>/api/v1/devices` → `401`;
  with an admin token → `200`; with a member token → `403`;
  `/api/v1/logs` with an admin token → `403`; `https://app.<domain>/` → `404`.
- On the LAN: `curl https://app.<domain>/api/v1/health` resolves to the LAN IP and
  presents a valid certificate (`curl` without `-k`).
- Dashboard on `127.0.0.1:8080` works exactly as before.
- `openapi_test.go` passes; revocation takes effect on the next request.
