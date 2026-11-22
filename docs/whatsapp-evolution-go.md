# WhatsApp through Evolution Go: keys and verification status

The full operator runbook is [docs/hub-whatsapp-evolution.md](hub-whatsapp-evolution.md).
This page records what the hub's adapter (`internal/hub/alerts/adapters/evolution`)
expects, and what has been checked.

Evolution Go is **not** the older Evolution API (Node). Its README still lists
the Node routes (`/message/sendText/{instance}`…), but its code does not serve
them. The hub follows the code.

| What | Route | Key in the `apikey` header |
|---|---|---|
| Send an alert | `POST /send/text` `{"number":"221771234567","text":"…"}` | instance token |
| Health | `GET /instance/status` → `data.Connected`, `data.LoggedIn` | instance token |
| Answers | `MESSAGE` events posted to `<hub>/whatsapp/v1/inbound/<KERGUI_HUB_WEBHOOK_SECRET>` | — |

- `KERGUI_HUB_EVOLUTION_APIKEY` is the **instance token** chosen at
  `POST /instance/create`, **not** `GLOBAL_API_KEY` (that one opens the admin
  routes only, and the instance routes refuse it with 401).
- `KERGUI_HUB_EVOLUTION_INSTANCE` is the instance's **name**: the hub refuses
  webhook events naming another instance, or carrying another `instanceToken`.
- Operator steps, in short: run Evolution Go (`GLOBAL_API_KEY` in its `.env`,
  `WEBHOOK_URL` left empty) → activate the licence at `/manager/login` →
  `POST /instance/create` `{"name","token"}` with `GLOBAL_API_KEY` → scan
  `GET /instance/qr` → `POST /instance/connect`
  `{"webhookUrl":"<hub>/whatsapp/v1/inbound/<secret>","subscribe":["MESSAGE"]}`
  with the instance token. Details in the runbook.

## Verified

- **Against the source** (`evolution-foundation/evolution-go`, `main`, read
  2026-11-22): routes and keys above, the `/send/text` body, the
  `/instance/status` answer, `503 LICENSE_REQUIRED` with `register_url`, the
  `/instance/connect` body, and the `MESSAGE` event (`event:"Message"`,
  `instanceName`, `instanceToken`, whatsmeow's `Info`, text in
  `Message.conversation` or `Message.extendedTextMessage.text`). The adapter's
  tests run against a fake server reproducing that behaviour.
- **Against a live, not yet activated instance** (`evoapicloud/evolution-go`,
  2026-11-22): the routes exist, `/message/sendText` does not, and every route
  answers the `503 LICENSE_REQUIRED` body above; the adapter reports it as
  "not activated" with the Manager URL.
- **Not yet verified live** (needs an activated, linked instance): the exact
  `/send/text` answer (id read from `data.Info.ID`, absence tolerated), the
  `401` on a wrong token, a real `MESSAGE` event, senders known only by their
  LID (ignored: no number to match) and the quoted id a reply carries.
