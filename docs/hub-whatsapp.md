# WhatsApp in the hub (whatsmeow)

The hub sends and receives WhatsApp itself, through
[whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0), the Go library
that WhatsApp Web clients are built on (issue #59). There is no separate
service and no webhook. The hub is linked to a **dedicated** WhatsApp number
as a "connected device", the way WhatsApp Web is.

What goes through it: sign-in codes, invitations, new-device alerts, and the
replies to them: « 1 » blocks, « 2 » or « OK » approves, `KERGUI <code>`
links a number, and STOP ends the alerts.

## Configuration

| Variable | Value |
|---|---|
| `KERGUI_HUB_NOTIFIER` | `whatsmeow` (`console` logs the messages instead, for tests and pilots) |
| `KERGUI_HUB_WHATSAPP_NUMBER` | the Kergui number in E.164, e.g. `+221770000000`. The `wa.me` opt-in links point at it |
| `KERGUI_HUB_KEY_SEED` | optional. Without it, a « 1 » reply does not block anything |

`KERGUI_HUB_WEBHOOK_SECRET` is not needed with `whatsmeow`.

## The session

- It is stored in the hub's Postgres, in the `whatsmeow_*` tables, so a
  redeploy keeps the number linked. The tables are part of the backups.
- **Run one hub only.** Two processes on the same session take it from each
  other, and the hub logs "another process took over the WhatsApp session".
- whatsmeow reconnects on its own after a network drop.
- Until a number is linked, or after it was unlinked from the phone, the
  WhatsApp messages are not sent. The households are still alerted by push,
  and the hub logs why.

## Linking the number (once)

The link is made from the running hub, through routes that only platform
administrators can call (JWT with the `ROLE_ADMIN` platform role):

```sh
TOKEN=...   # a platform admin's access token
HUB=https://hub.<domain>

# 1. State
curl -s -H "Authorization: Bearer $TOKEN" $HUB/api/v1/admin/whatsapp
# {"linked":false,"connected":false}

# 2. Ask for a pairing code (without a body, it uses KERGUI_HUB_WHATSAPP_NUMBER)
curl -s -X POST -H "Authorization: Bearer $TOKEN" $HUB/api/v1/admin/whatsapp/link
# {"code":"ABCD-EFGH", ...}
```

3. On the phone that has the Kergui number, open WhatsApp → **Appareils
   connectés** → **Connecter un appareil** → **Connecter avec un numéro de
   téléphone**, then type the code. It is valid for a few minutes.
4. `GET /api/v1/admin/whatsapp` now answers `{"linked":true,"number":"+221…","connected":true}`.

To change the number: `DELETE /api/v1/admin/whatsapp` (logs the hub out and
forgets the session), then link again.

## Risks

- whatsmeow is not an official WhatsApp API, so the number can be banned. Use
  a number kept for Kergui, never a person's own. Only send what the
  households asked for: they write first (`KERGUI <code>`), and STOP is
  honoured.
- Licence: MPL-2.0 only asks that changes to whatsmeow's own files be
  published. We use it as it is, and there is no notice to show in the
  product.
