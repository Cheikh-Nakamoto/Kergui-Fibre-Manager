# RFC-007 — Closed Wi-Fi: only the household's devices may join

- Status: **Accepted** (2026-11-26). Decisions settled with the owner in a design interview the
  same day (table "Decisions of 2026-11-26" at the end). The box writes still wait for the live
  write session (W1–W9).
- Builds on: [RFC-001](RFC-001-router-adapter.md) (optional router capabilities),
  [RFC-005](RFC-005-semi-distributed-hub.md) (relay routes, trust, #94, #96),
  [RFC-006](RFC-006-freemium-subscriptions.md) (tiers, auto-reject).
- Amends: RFC-006 § "Auto-reject after 15 minutes": an automatic refusal becomes a **firewall** rule
  (« Couper internet »), no longer a Wi-Fi access-control rule (D5). It also amends the owner rule of
  2026-11-23 in `mobilecore/approvals.go`: devices are approved by themselves only while the Wi-Fi is
  open (D1).

## Context

« Couper l'accès », auto-reject and WhatsApp « 1 = bloquer » each add a **deny** rule to the box.
The ZTE F6600P holds a limited number of rules, and a deny list never stops growing: every
stranger, every new private address of a phone and every automatic refusal takes a slot, until
the box refuses to add more.

The F6600P's Wi-Fi access control has a per-access-point mode: `Disabled` / `Ban` / `Allow`. In
`Allow`, only the listed devices may associate. A stranger with the password or the QR code is
refused anyway. A home has 10 to 25 devices, so an allow list stays small and stable. This RFC
specifies how kergui uses it without ever locking the owner out.

Customer-facing words (French, `mobile/src/i18n/fr.ts`): « Wi-Fi ouvert / fermé », « Fermer /
Ouvrir mon Wi-Fi », « Autorisé / À vérifier / Bloqué », « Ajouter un nouvel appareil », « Couper
internet », « Le déconnecter aussi du Wi-Fi ». Never « liste blanche », « liste noire », « filtrage
MAC », « ACL », « whitelist ». In this RFC "allow list" and "deny rule" are engineering terms only.

## Step 0 — what the live box says

### Read on 2026-11-26 (read-only, firmware `ZTEGF660006SN`)

Read from the household's box, account `user`, GETs only: `menuView wlanAdvanced`,
`wlan_macfilteraclpolicy_lua.lua`, `wlan_macfilterrule_lua.lua`, `menuView localNetStatus`,
`wlan_wlanstatus_lua.lua`, `menuView filterCriteria`, `firewall_macfilterv3_lua.lua`,
`firewall_filterglobal_lua.lua`, `menuView wlanBasic`, `wlan_wlansssidconf_lua.lua`. Sanitised
captures (every MAC and BSSID replaced, broadcast names replaced) are in
`internal/fixtures/zte_f6600p/`: `wlan_macfilteraclpolicy.xml`, `wlan_macfilterrule.xml`,
`wlan_wlanstatus.xml`.

| # | Finding | Consequence |
|---|---|---|
| R1 | 8 APs: AP1–AP4 on the 2.4 GHz radio, AP5–AP8 on 5 GHz. **3 enabled**: AP1 (2.4 GHz), AP5 (5 GHz), and AP7 (5 GHz only, a different, shorter name). | "All the household's APs" is not "both bands of SSID1": AP7 exists and kergui today writes to it too. |
| R2 | Modes now: AP1, AP5, AP7 = `Ban`; the others `Disabled`. | The current deny rules are live on all three. |
| R3 | **One shared rule table** (`OBJ_ACLCFG_ID`, `DEV.ACLn`). Each rule = one MAC **bound to one AP** (`Interface`). | One allowed device costs **one rule per closed AP**. |
| R4 | **63 rules = 21 MACs × 3 APs.** All 63 follow kergui's own name pattern `<mac> APn`, so kergui created every one. | The table is filling up the way the problem says. kergui can recognise its own rules. |
| R5 | **19 of the 21 MACs are locally administered** (bit `0x02` of the first octet): private, rotating phone addresses. | Most deny rules are for addresses that will never come back. The purge frees most of the table. |
| R6 | `_InstID`s are not contiguous (gaps at ACL40–42 and ACL52–54). | The "renumbered after each deletion" note in `wifi.go` is not the whole story. The adapter already re-reads before each delete, which stays mandatory. |
| R7 | No rule limit appears anywhere in the page's HTML or JS (no `maxInstNum` on the Wi-Fi rule form). `MaxUserNum = 32` is the **associated-station limit per AP**, not a rule limit. | The limit must be **measured** (W1). The "32 places" of the mock-up must not be hard-coded. |
| R8 | The SSID page (`wlan_wlansssidconf_lua.lua`) has **no `GUESTWIFI` object** on this box, and lists AP1, AP3, AP4, AP5, AP7, AP8 (AP2 and AP6 absent). | Nothing on this box marks a "guest" network: the household says which one is for visitors (D2). |
| R9 | Firewall MAC filter: on, `MacFilterTarget = Discard`, **11 rules**. | The firewall list grows too, and is purged with the same rule. |
| R10 | The page offers « No Filter / Liste noire / Liste blanche » per AP (`Disabled` / `Ban` / `Allow`). | Confirms the mode is per AP. |

**The trap (R2 + R3):** a rule means "refuse" on an AP in `Ban` and "admit" on an AP in `Allow`.
Flipping an AP from `Ban` to `Allow` as it stands would **admit the 21 refused devices and
refuse everyone else, the owner included**. Flipping it back to `Ban` for the add window would
**refuse every authorised device**. An AP cannot hold deny rules and allow rules at the same time.
The sequences below exist because of this.

### Still to run on the box: the live write session

Owner present, a PC on a **cable** (the PC that did the reading is on the box's Wi-Fi, so nothing
was written), a spare phone as the test device. Everything happens on a **spare test network**,
so that the household's AP1/AP5/AP7 are never touched: enable AP3 (2.4 GHz, now disabled) as
`Kergui-Test` with its own password, and save a snapshot of policies and rules first.

| # | Test | Records |
|---|---|---|
| W1 | Add rules with fake private MACs (`02:00:00:00:00:xx`) on AP3 until the box refuses. | The limit, whether it is global (the 63 count) or per AP, and **the box's exact answer when full** (`IF_ERRORSTR`, HTTP status). Then delete them. |
| W2 | AP3 in `Allow` with **no rule**. Test phone tries to join. | Does the box accept the mode? Is the phone refused? |
| W3 | Test phone joined on AP3 in `Disabled`, then switch to `Allow` without it. | Is an already-connected device kicked off? How fast? |
| W4 | Add the test phone's MAC, rejoin. | That `Allow` admits it. |
| W5 | After W2/W3, look at the box's logs and `wlan_client_stat`. | Whether a refused device leaves a readable trace. |
| W6 | Look for a « Wi-Fi invité » page in the box's menu. Check AP2 / AP6. | Whether a visitors' network is a separate AP, and how to recognise it. |
| W7 | Same as W1 on the firewall MAC filter. | Firewall limit and its full answer. |
| W8 | `WiFiBlock` / `WiFiUnblock` on AP3 with the test phone. | Lets `wifi_block` / `wifi_unblock` become `Verified` (they are not, #96). |
| W9 | Restore: delete test rules, AP3 back to `Disabled`, compare with the snapshot. | Box exactly as before. |

Each result is dated in `docs/routers/zte-f6600p.md`. A `Verified` flag flips only after the dated
run, with the sanitised capture added to the fixtures.

## Decision

### D1. One vocabulary, one piece of data

Each device is in exactly one of three states, for the household and in the code:

| State (app) | Means | Stored as |
|---|---|---|
| ✓ Autorisé | « Peut toujours se connecter » | the existing **approval** (#58: `mobilecore/approvals.go`, synced with the hub's `hub_inventory_approvals`) |
| ? À vérifier | « Ni autorisé ni bloqué » (default) | neither |
| ⛔ Bloqué | « Ne peut plus se connecter » | a block (D5), with or without « Le déconnecter aussi du Wi-Fi » |

- "Autoriser" **is** today's "approve": the notification, WhatsApp « OK », the device sheet, and
  auto-reject's `Accept` all write the same approval. Auto-reject's accepted set is fed from it.
- A device cannot be both Autorisé and Bloqué. Authorising a blocked device unblocks it; blocking
  an authorised one withdraws the approval.
- **Self-approval after 15 minutes** (owner rule of 2026-11-23) runs **only while the Wi-Fi is
  open**, where it writes nothing on the box. While the Wi-Fi is closed or an add window runs, a
  device gets in only by a hand « Autoriser » (app) or « OK » (WhatsApp, D9).

### D2. Which access points are closed

- **Household APs** = every enabled AP except the visitors' ones.
- **Visitors' APs**: the box does not mark them (R8). At the first « Fermer mon Wi-Fi », the panel
  asks once, for each enabled network other than the one this phone is on (and its other-band
  twin with the same name), « Ce réseau est-il pour vos visiteurs ? ». The answer is kept, and can
  be changed in Réglages. A visitors' network stays open by default, with its own password. An
  explicit option « Fermer aussi le Wi-Fi des visiteurs » closes it too.
- Devices on a **cable** are never affected by the Wi-Fi list. That is the way back in, and the
  help says so.

### D3. Capacity ("places")

`places = floor((limit − rules on APs that stay open) / number of closed APs)`. `limit` is
measured per model (W1) and declared by the adapter. With a hypothetical limit of 64 and 2 closed
APs, that is 32 places; with 3 closed APs, 21. The « Autorisés » tab shows `N / places`, orange
from 80 %. Until W1 has run, the adapter declares the limit as unknown, and the app shows the
count without a maximum. When the Wi-Fi is closed, only allow rules use the table: the Wi-Fi
refusals are kept in kergui, not on the box (D5).

### D4. Closing, opening, and the add window on the F6600P

The adapter converges the box to a **target state** (per AP: mode + rule set) rather than running
a one-shot script, so an interrupted run (the relay and the phone take the single admin session
from each other, #94) is finished or undone by the next writer.

The household APs are in one of three box states:

| Box state | Mode of the household APs | Rules on them |
|---|---|---|
| **Open, light** | `Disabled` | the allow list, ignored by the box |
| **Open, with Wi-Fi refusals** | `Ban` | one deny rule per « déconnecté du Wi-Fi » device |
| **Closed** | `Allow` | the allow list |

**Close** (`Fermer mon Wi-Fi`):

1. Snapshot policies and all rules. Compute the target list: the household's approvals **+ this
   phone + every relay of the household + every phone marked Owner** (`mobilecore/owners.go`).
   Those are always in the list and cannot be unchecked. Check the capacity **before any write**:
   if it does not fit, answer « liste pleine » with the numbers and write nothing.
2. Set the household APs to `Disabled` (one signed POST of the policy form).
3. Delete every deny rule on them. The Wi-Fi refusals stay recorded in kergui (D5). Re-read the
   table before each delete.
4. Add one allow rule per MAC per household AP that does not have it yet.
5. Read the table back. It must equal the target, **this phone's MAC included**; otherwise roll
   back.
6. Set the household APs to `Allow` (one POST). Read back policies and rules.
7. On any failure: back to the snapshot (rules re-created, previous modes restored), then read
   back. The journal says what happened.

From "open, light", steps 3–4 only add or remove the difference: a close is a few POSTs. Between
steps 2 and 6 (seconds to tens of seconds, measured in the write session) the household APs
filter nothing. A blocked device may associate during that time but stays without internet: every
block carries a firewall rule (D5).

**Open** (`Ouvrir mon Wi-Fi`, always free), in two steps so nobody waits:

1. Set the household APs to `Disabled`, keeping the allow rules (seconds). The button shows
   « Wi-Fi ouvert ».
2. If at least one device is « déconnecté du Wi-Fi », kergui re-establishes those refusals in the
   background: delete the allow rules, write the deny rules, set `Ban`. Their badges show « … En
   cours » until the box is read back. Meanwhile they are on the firewall rule, so without internet.

**Add window** (`Ajouter un nouvel appareil (5 min)`, Wi-Fi closed): set the household APs to
`Disabled` for 5 minutes, allow rules kept, then back to `Allow`: two POSTs.

- During the window the panel offers « Encore 5 min » and « Refermer maintenant ». The window
  never lasts more than 15 minutes in all.
- The new device joins, appears « À vérifier » with an alert. « Autoriser » (app, or « OK » on
  WhatsApp, D9) adds its allow rule. There is no self-approval during the window (D1).
- A device « déconnecté du Wi-Fi » can associate during the window, but stays without internet
  (its firewall rule).
- The button shows « Wi-Fi ouvert pour 3 min » (minutes left, rounded up).
- At the deadline the box is closed again by, in order of availability: the household's relay, the
  phone's watch service, or the app when it comes back to the foreground. The deadline is stored in
  the core, so a killed app re-closes on its next start.

**If nobody closes it again** (no relay, phone away or the watch service off, as on iOS with the
app closed): the household APs stay `Disabled`, which is the box as it was before kergui. Blocked
devices still have no internet, but any newcomer with the password can join.
- The button shows the box's real state, « Wi-Fi ouvert », with one line « L'ajout est terminé :
  fermez à nouveau votre Wi-Fi ».
- 10 minutes after the deadline the household gets a notification and, with a relay, a WhatsApp
  message « Votre Wi-Fi est resté ouvert ».
- The first enforcer that runs after the deadline closes it again without asking, since the
  household had closed it.

**The box is the truth for display.** The button always shows what the box says. The stored
"closed" intent is used only to finish an interrupted close and to end an add window. If the box
is found open without an add window (box reset, someone used the box's web page), the app shows
« Wi-Fi ouvert », writes it in the journal, and **never** closes it on its own.

### D5. Bloqué: « Couper internet », and optionally « Le déconnecter aussi du Wi-Fi »

Two ways to block, chosen per device:

- **« Couper internet »** (default): a firewall rule (`MacFilterTarget = Discard`). One rule per
  device whatever the number of APs, verified live 2026-10-10, and it also covers cable devices.
  The device stays « connecté, sans internet ».
- **« Le déconnecter aussi du Wi-Fi »**: a switch in the « Bloqué » panel of each device and on its
  sheet. It adds the Wi-Fi refusal **on top of** the firewall rule, so the device never gets
  internet back, even during an add window or on a cable. The badge stays « ⛔ Bloqué », with a
  small line « sans internet » or « refusé au Wi-Fi ».

Auto-reject and WhatsApp « 1 » have no switch: they use « Couper internet ».

| Action | Wi-Fi ouvert | Wi-Fi fermé |
|---|---|---|
| Autoriser | approval only; nothing written on the box | approval + allow rule on each household AP |
| À vérifier | approval withdrawn; nothing on the box | approval withdrawn + allow rule removed (the device is refused) |
| Bloquer (« Couper internet ») | firewall rule | firewall rule + allow rule removed |
| « Le déconnecter aussi du Wi-Fi » on | + deny rule on each household AP (box goes to "open, with Wi-Fi refusals", D4) | recorded in kergui only (it is refused anyway); written back at the next open |
| Débloquer | firewall rule and Wi-Fi refusal removed → « À vérifier » | firewall rule removed, refusal forgotten → « À vérifier » (still refused until authorised) |
| Auto-reject | as RFC-006, refusing with a **firewall** rule | **paused**: it has nothing left to do. Its screen (`auto-reject.tsx`) says so. |
| WhatsApp « 1 » | firewall rule | firewall rule + allow rule removed |

- The separate « Couper l'accès » button disappears: it becomes the switch.
- This phone: « Bloqué » is disabled with « C'est ce téléphone », and so is « À vérifier » while the
  Wi-Fi is closed, since it would refuse the phone too.
- After « Bloqué » or « À vérifier », a banner « Bloqué · Annuler » shows for 5 seconds. There is no
  confirmation dialog, except when closing the Wi-Fi.

### D6. Purge of stale rules and « liste pleine » (for current customers now)

- Removes a **kergui-made** rule (Wi-Fi: name `<mac> APn`; firewall: rules kergui wrote) when its
  MAC is in the inventory with `last_seen` older than **30 days**.
- A MAC the inventory does not know is **never removed automatically** (a reinstalled app has an
  empty inventory and must not unblock everyone). It is only offered for removal by hand in the
  « liste pleine » screen.
- Rules the household made in the box's web page are never touched.
- The Wi-Fi refusals that stay (device seen in the last 30 days) become « Bloqué · refusé au
  Wi-Fi » and get their **missing firewall rule** (D5).
- Runs on the device that holds the inventory (the phone's core, or the relay), once a day and right
  before a block that would not fit. Each removal is a journal line « Règle retirée : appareil
  absent depuis 30 jours ».
- A refusal because the list is full answers `domain.ErrAccessListFull` (with the kind, the limit
  and the count). The app shows « La box accepte N appareils au plus. Retirez un appareil que vous
  n'utilisez plus. » instead of the raw box error. The box's exact text comes from W1/W7.
- Removing Wi-Fi rules is a `wifi_unblock`, unverified until W8: that part (ticket 1b) waits for
  the write session. Ticket 1a does the firewall purge, the missing firewall rules and the
  message, and **lists** the stale Wi-Fi rules in the journal.

### D7. Domain and port

```go
// internal/domain
var ErrAccessListFull = errors.New("router: access list full")
type AccessListFullError struct{ Kind string; Limit, Used int } // Kind: "wifi" | "firewall"; wraps ErrAccessListFull

type WiFiLockAP struct{ ID, Band, Name string; Mode string /* Disabled|Ban|Allow */; Visitors bool }
type WiFiLockBox struct {                // what the box says
    APs   []WiFiLockAP
    Rules map[string][]MAC               // per AP, whatever its mode
    Limit int                            // 0 = unknown
    Used  int
}
type WiFiLockTarget struct {             // what kergui wants on the household APs
    APs     []string
    Mode    string                       // Disabled | Ban | Allow
    MACs    []MAC                        // allow list (Allow, Disabled) or deny list (Ban)
}

// internal/usecase/port — optional capability, asserted like WiFiAccessControl
type WiFiAllowList interface {
    WiFiLockBox(ctx context.Context) (domain.WiFiLockBox, error)
    // ApplyWiFiLock converges the household APs to t (D4: Disabled first, rules, read-back,
    // then the mode), with a snapshot and rollback on any failure.
    ApplyWiFiLock(ctx context.Context, t domain.WiFiLockTarget) error
    // SetWiFiMode flips the mode only, rules kept (open light, add window, re-close).
    SetWiFiMode(ctx context.Context, aps []string, mode string) error
}
```

The use case owns the guarantees: this phone, the relays and the Owner phones always in the
target; the capacity preview; the add-window deadline and its extensions; D5's mapping; the
background re-establishment after an open; no self-approval while closed. The adapter owns the
box's ordering.

The stored intent lives in the core's database and in the backup: `closed`, household and
visitors' APs, `visitorsClosed`, `windowUntil`, `since`, and the per-device « déconnecté du Wi-Fi »
flag.

`Meta()` gets `wifi_lock` (read) and `wifi_lock_write` (write), both `Verified: false` until W2–W4
have run. Through the relay, the writes are refused until then (#96).

The mock (`internal/mockrouter/f6600p.go`) implements `Allow` with the shared table, the per-AP
binding, the "rules invert" behaviour, kicking unlisted stations on the switch (per W3, assumed
yes until measured), and a configurable limit with a full answer.

### D8. Tiers (`internal/entitlement/tier.go`, mirrored in `gate.ts`)

Following "giving access back is free" (RFC-006):

| Tier | Features |
|---|---|
| **Free** | `wifi_unlock` (Ouvrir mon Wi-Fi, even with a lapsed pass), Autoriser, Débloquer (`unblock`, `restore_wifi_access`) |
| **Basic** | Bloquer (`block`); « Le déconnecter aussi du Wi-Fi » (`cut_wifi_access`); « À vérifier » on an authorised device while closed (it refuses it) |
| **Max** | `wifi_lock`: Fermer mon Wi-Fi, Ajouter un nouvel appareil (and its « Encore 5 min ») |

A pass that lapses while the Wi-Fi is closed changes nothing on the box. The household can still
authorise, unblock and open. Only closing again needs Max.

### D9. Relay, WhatsApp and trust

- New relay routes, added on purpose to `TestAllowList_IsExactlyIssue50` and RFC-005:
  `GET /api/wifi/lock`, `POST /api/wifi/lock/close`, `POST /api/wifi/lock/open`,
  `POST /api/wifi/lock/window` (open, extend, close now), and `PUT` / `DELETE /api/approvals/+`, so
  that a co-owner away from home can authorise a new phone (D10). They are refused as unverified
  (#96) until the write session.
- **Closing from away** (the phone on 4G, through the relay) is allowed. The condition « ce
  téléphone est autorisé » becomes « le relais de la maison est autorisé » (it always is). If this
  phone is not authorised, the panel warns that it will be refused when it comes home.
- During an add window the relay closes the box at the deadline, and sends its heartbeat with
  `wifiLock: closed|open|window`. The hub sends « Votre Wi-Fi est resté ouvert » from it (D4).
- **WhatsApp « OK » during a window.** In closed mode the approval must reach the box, and the hub
  is not trusted to admit a device (RFC-005: it only transports). So:
  - the **owner signs the window** (box, `windowUntil`), and signs again for each « Encore 5 min »;
  - the relay accepts a hub-forwarded « OK » **only for a MAC it saw arrive itself during that
    signed window**.

  That gives the hub no power it did not already have: during the window anyone with the password
  can join anyway. Outside a window, a WhatsApp « OK » while closed answers « Ouvrez l'application
  pour autoriser cet appareil ».

### D10. Lost phone, new phone, backup

- A new phone is not authorised. `recovery.tsx` explains the three ways in:
  - a co-owner authorises it from their app (also from away, D9);
  - plug a device in by cable and open the Wi-Fi;
  - « Ouvrir mon Wi-Fi » from any authorised device.
- The household backup (`mobilecore/backup.go`) keeps the open/closed intent, the visitors' APs,
  the « déconnecté du Wi-Fi » flags and the approvals (it already restores named devices as
  approved). Restoring never closes the box by itself.
- **Phones on a private address.** An address that changes means the phone is refused at home.
  - The device sheet shows « Adresse privée : peut changer » under the name of any device whose
    address is locally administered (bit `0x02` of the first octet).
  - When this phone's address is private, the closing panel adds one line, « Vérifiez que
    l'adresse privée de ce téléphone est fixe pour ce Wi-Fi », with a help link (Android « Adresse
    MAC de l'appareil » / iOS « Adresse privée : fixe »). It does not block the close.

### D11. Other routers

`port.WiFiAllowList` is optional. Without it, the app hides the button, the « Autorisés » tab and
the « Autorisé » state of the badges (the badges then show only « À vérifier » / « Bloqué »).
Without `port.WiFiAccessControl`, the « Le déconnecter aussi du Wi-Fi » switch is hidden.

### D12. The app

The owner's design, with the decisions above:

- **The status button**, in the devices tab's `headerLeft` (`(tabs)/_layout.tsx`). It always shows
  the box's state, never the action: `🔓 Wi-Fi ouvert` (grey), `🔒 Wi-Fi fermé` (green),
  `… En cours`, and `Wi-Fi ouvert pour N min`.
- **The bottom panel.** Touching the button opens it; nothing changes without a second gesture.
  - It uses the owner's texts, and asks once about the visitors' networks (D2).
  - `Fermer mon Wi-Fi` stays disabled until this phone is authorised (« Autorisez d'abord ce
    téléphone »; through the relay: D9), with a lock on the button when not on Max. The lock
    shows the app's neutral sentence and **opens nothing**: no offer, price or link (RFC-006 « No
    payment in the app », Play's payments policy; UX audit 2026-11-26, R1).
  - During a window it offers « Encore 5 min » and « Refermer maintenant ».
- **The per-device badge** (`✓ Autorisé`, `? À vérifier`, `⛔ Bloqué`; colour, icon **and** word).
  - It opens a small panel with the three choices and their phrase, the current one checked, and
    the « Le déconnecter aussi du Wi-Fi » switch under « Bloqué ».
  - On the device sheet (`mobile/src/app/device/`) the same choice is a segmented control
    `Autorisé | À vérifier | Bloqué`, plus the switch.
- **When a badge changes.** It changes state only after the box is read back, showing « En cours… »
  meanwhile. In open mode, Autoriser and À vérifier write nothing on the box and change at once.
- **The « Autorisés » tab** lists absent devices too, shows the place counter (orange from 80 %),
  and the « liste pleine » message.
- **The first close.** If fewer than 2 devices are authorised, `Fermer mon Wi-Fi` first opens
  « Voici les appareils de la maison ».
  - It lists the devices seen in the last 30 days and not blocked, all checked.
  - This phone, the relay and the Owner phones are checked and cannot be unchecked.
  - The list is capped at the places: if there are more, the oldest-seen are unchecked first.
  - `Autoriser ces N appareils` saves them, then the closing panel comes back.

## Tickets (GitHub, in order, one sub-agent each)

- **Parent**: RFC-007, never processed by the loop.
- **Live write session** (W1–W9), for the owner, not `ready-for-agent`.
- **1a** Firewall purge, missing firewall rules for kept Wi-Fi refusals, `ErrAccessListFull` and
  « liste pleine ».
- **1b** Wi-Fi purge. Blocked by 1a and the live session (W8).
- **2** Domain and port (D7): state, approvals as the single source, capacity, self-approval only
  while open. Blocked by 1a.
- **3** F6600P adapter + mock (D4), `Meta()` unverified, integration tests against the mock: the
  trap, rollback at each step, an interrupted run, a full list, this phone always present. Blocked
  by 2.
- **4** Use cases: close (preview, rollback), open (light, then background refusals), the add
  window and its extensions, D5's mapping, auto-reject paused while closed and on firewall rules
  while open. Blocked by 3.
- **5** `mobilecore`: routes, `wifi_lock` / `wifi_unlock`, demo mode, route allow list. Blocked by 4.
- **6** App (D12). Blocked by 5.
- **7** Relay and WhatsApp (D9). Blocked by 6.
- **8** Backup, lost phone, docs (`zte-f6600p.md`, RFC-005, RFC-006), site. Blocked by 7.

`make check` green at each ticket.

## Decisions of 2026-11-26 (design interview with the owner)

| # | Question | Decision |
|---|---|---|
| Q1 | Mode during the add window | `Disabled`, not `Ban` (the trap) |
| Q2 | How a device is blocked while the Wi-Fi is open | Both, per device: « Couper internet » by default, switch « Le déconnecter aussi du Wi-Fi » |
| Q2b | Auto-reject and WhatsApp « 1 » | « Couper internet » |
| Q2c | Does the Wi-Fi refusal also cut internet? | Yes, both rules |
| Q2d | Opening with Wi-Fi refusals present | Fast open, refusals re-established in the background |
| Q3 | Visitors' network | Asked once, kept, changeable in Réglages |
| Q4 | Blocked MACs the inventory does not know | Never purged automatically, only by hand from « liste pleine » |
| Q5 | Closing from away | Allowed when the relay is authorised |
| Q6 | WhatsApp « OK » during a window | Owner-signed window, relay checks it saw the device arrive |
| Q7 | Tiers | D8 table |
| Q8 | Live write session vs tickets | Tickets first; the box-writing parts wait for the session |
| G1 | This phone on a private address | Advice line in the closing panel, no blocking |
| G2 | Co-owners' phones | Always in the list, like this phone |
| G3 | Wi-Fi refusals already on the boxes | Purge the stale ones, add the missing firewall rule to the others |
| G4 | Add window length | 5 min, « Encore 5 min », 15 min at most |
| G5 | Window nobody closes / box found open | Notification + WhatsApp after 10 min; a box found open without a window is never closed by kergui |
| G6 | Self-approval after 15 min | Only while the Wi-Fi is open |

## Consequences

- The Wi-Fi rule table stops growing with strangers: it holds the household's devices × closed
  APs, plus the few devices the household chose to « déconnecter du Wi-Fi ».
- The firewall list holds only blocked devices seen in the last 30 days.
- The owner can be refused only if this phone's address changes. The cable, a co-owner and
  « Ouvrir » from any authorised device are the ways back, and none of them needs a pass.
- An add window that nobody closes leaves the box as it was before kergui, never worse, and the
  household is told.
- On the F6600P, the first close rewrites the table once. Afterwards opening, re-closing and the
  add window are mode flips. Only an open with Wi-Fi refusals present rewrites the table, in the
  background.
