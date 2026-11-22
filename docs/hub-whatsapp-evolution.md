# Running the hub's WhatsApp through Evolution Go

The hub sends WhatsApp messages (sign-in codes, invites, alerts) through
**Evolution Go**, a separate service it calls over HTTP. Nothing of Evolution
Go is built into Kergui: `deploy/hub/compose.yaml` runs its official image
(`evoapicloud/evolution-go`, pinned by digest) next to the hub, under the
compose profile `evolution`, with its own Postgres. The routes and keys the
adapter uses are described in [whatsapp-evolution-go.md](whatsapp-evolution-go.md).

Evolution Go drives a WhatsApp Web session, not Meta's official API: use a
**dedicated number**, never a personal one, since it can be banned.

## 1. Secrets (`deploy/hub/hub.env`)

```sh
KERGUI_EVOLUTION_DB_PASSWORD=$(openssl rand -hex 24)
KERGUI_EVOLUTION_GLOBAL_API_KEY=$(openssl rand -hex 24)   # admin key, Manager login
```

The global key creates instances and opens the Manager. The hub never uses
it: it sends with the instance's own token (step 4).

## 2. Start it

```sh
docker compose -f deploy/hub/compose.yaml --env-file deploy/hub/hub.env \
  --profile evolution up -d
```

Its Manager is published on `http://127.0.0.1:8081` only (the container listens
on 8080). Until its licence is activated, every API route answers
`503 LICENSE_REQUIRED`.

## 3. Activate the licence (a person, once)

Open `http://127.0.0.1:8081/manager/login`, enter the API URL
(`http://127.0.0.1:8081`) and the global key, and complete Evolution
Foundation's registration. `GET /license/status` then reports `active`.

## 4. Create the instance and pair the phone

```sh
EVO=http://127.0.0.1:8081
GLOBAL=...            # KERGUI_EVOLUTION_GLOBAL_API_KEY
TOKEN=$(openssl rand -hex 24)   # the instance token: the hub's key
curl -s -X POST $EVO/instance/create -H "apikey: $GLOBAL" \
  -H 'Content-Type: application/json' -d "{\"name\":\"kergui\",\"token\":\"$TOKEN\"}"
curl -s $EVO/instance/qr -H "apikey: $TOKEN"      # QR to scan
```

On the dedicated phone: WhatsApp → Settings → Linked devices → Link a device,
and scan the QR (the Manager shows it too). `GET /instance/status` with the
token must then answer `Connected` and `LoggedIn` true.

## 5. Point its webhook at the hub

Inside the compose network the hub is `http://hub:8090`:

```sh
curl -s -X POST $EVO/instance/connect -H "apikey: $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"webhookUrl\":\"http://hub:8090/whatsapp/v1/inbound/$KERGUI_HUB_WEBHOOK_SECRET\",\"subscribe\":[\"MESSAGE\"]}"
```

Leave Evolution Go's global `WEBHOOK_URL` unset: every event carries the
instance token, so it goes to the hub only.

## 6. Switch the hub to it

In `hub.env`:

```sh
KERGUI_HUB_NOTIFIER=evolution
KERGUI_HUB_WHATSAPP_NUMBER=+221...        # the dedicated number, E.164
KERGUI_HUB_EVOLUTION_URL=http://evolution-go:8080
KERGUI_HUB_EVOLUTION_INSTANCE=kergui
KERGUI_HUB_EVOLUTION_APIKEY=<TOKEN>       # the instance token, not the global key
```

then `docker compose ... --profile evolution up -d hub`. The hub's start-up log
and its WhatsApp settings route carry Evolution Go's attribution notice, as its
licence asks.
