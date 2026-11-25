# A phone left at home as the relay (Android, issue #45)

A phone that stays at home, on the box's Wi-Fi and on its charger, can be the
household's relay ([RFC-005](../rfcs/RFC-005-semi-distributed-hub.md#phone-relay-45)):
it holds the box's single admin session for the household and runs the
commands the owner signs from anywhere. This page is what the code does, how
Android's battery rules treat it, and what to check on a real phone.

## The path

| Step | Where | Code |
|---|---|---|
| « Être un relais » on the welcome screen, or Réglages → « Ce téléphone sert de relais » | JS | `mobile/src/app/index.tsx`, `mobile/src/ui/RelayPhoneSetting.tsx` |
| Scan the owner's QR (`expo-camera`), or type the code shown under it when the camera is refused | JS | `mobile/src/app/become-relay.tsx`, `mobile/src/app/relay.tsx` (`relay-invite-text`) |
| Pair: keys generated in the core, code redeemed, owner key pinned, six digits shown | Go | `mobilecore.RelayEnroll` → `agent.Enroll` |
| Wait for « Oui, c'est le même code » on the owner's phone | Go | `mobilecore.RelayCheckTrust` (polled every 3 s) |
| Ask for the notifications and the precise location, start the service | JS | `mobile/src/core/relayPhone.ts` |
| Follow the Wi-Fi, bind the process to it, find the box, run the loop | Kotlin | `RelayService.kt` (`connectedDevice`) |
| Find the box without the connection screen: the Wi-Fi's gateway, then the usual addresses, accepted only if its certificate is the pin the owner signed | Go | `mobilecore.RelayFindBox` |
| Heartbeat 20 s, lease, long poll of signed commands, signed results, device / observation / SSID snapshots, sealed password (#44), auto-reject (#36) | Go | `mobilecore.RelayStart` → `internal/agent` |
| Stand down cleanly: lease handed back to the hub at once | Go | `mobilecore.RelayStop` (service `onDestroy`, notification « Arrêter ») |

The relay phone never logs in to the box by hand: the password arrives sealed
from the owner's phone (`agent.pullCredentials`) and stays in its core's vault.

## The right network

The relay reads the network **the owner signed in the trust bundle** (`trust.Network`)
and checks it itself on every round (`agent.CheckNetwork`), on top of the
hub's own check:

| What the phone sees | Verdict | Shown |
|---|---|---|
| The box's certificate is the signed pin, and the Wi-Fi name (and access point, when registered) is the household's | works (`check: signature`) | « En service » |
| The box's certificate matches, no Wi-Fi name (location refused, or Android hides it) | works on the box identity alone (`check: box-identity`) | « En service (nom du Wi-Fi non vérifié…) » and, on screen, why |
| Another Wi-Fi, another access point, another box, or no box at all | **stands down**: every heartbeat says `released`, the hub never grants it the lease, and it never calls the box | « En pause : pas sur le Wi-Fi de la maison » |
| The right network, but the owner's phone holds the box at home (#94) | **pauses**: keeps the lease and its heartbeat (`paused`), leaves the box alone until the owner leaves | « En pause : le propriétaire est à la maison » |

The process is bound to the Wi-Fi (`bindProcessToNetwork`), and since Go's
sockets do not follow that call, every socket the core opens (box and hub) is
bound to the Wi-Fi too, through `RouterOptions.DialControl` →
`RelayNetwork.BindSocket` → `Network.bindSocket`. Off the Wi-Fi the bind is
refused: nothing reaches the box over mobile data.

## One box session

The box allows one admin session. On the relay phone, the watch service
(`WatchService`, alerts) and the relay service run in the same process on the
same Go core: every call that reaches the box takes the core's I/O lock and
uses its single shared session, so they never log each other out. While the
relay enforces auto-reject, the watch service skips its own round
(`relayStatus().autoReject`). Between devices, the hub's lease (one holder)
is what keeps relays apart.

The owner's phone at home talks to the box directly, and its session wins
(#94, « proprio prioritaire »). The relay sees the box name another address
than its own on the session it would evict, leaves it alone, and **pauses**:
it keeps its heartbeat and its lease (the app says « Relais en pause », never
« hors ligne »), reads nothing from the box and answers a member's command
« le propriétaire est à la maison ». The notification says « En pause : le
propriétaire est à la maison ». It takes the box back as soon as a command
signed by the owner reaches it through the hub (the owner's app goes through
the relay only once away), or, failing that, after 2 minutes once the owner's
session is gone from the box. See RFC-005, « Proprio prioritaire ».

## Battery and Doze

| Android behaviour | What the relay does |
|---|---|
| **Foreground service type.** `dataSync` is capped at 6 h per 24 h from Android 15. | `connectedDevice` (its prerequisite `CHANGE_NETWORK_STATE` is declared), with an ongoing, low-importance notification and an « Arrêter » action. |
| **Doze** (screen off, unplugged, still): the network is cut for apps outside maintenance windows, timers stretch. | A foreground service keeps network access in Doze; a partial wake lock keeps the 20 s heartbeat on time. **Keep the phone on its charger**: Doze does not start while charging, and the relay is meant to be plugged in. |
| **App Standby buckets / battery optimisation.** Vendor builds (ColorOS, MIUI, One UI) kill "optimised" apps' services. | Ask « Sans restriction » for Kergui (the same `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` prompt as the alerts, Réglages → batterie). The screen says so (`relayPhone.battery`). |
| **Reboot / app update.** | `BootReceiver` restarts the service if it was on (`RelayConfig.enabled`). |
| **Swiped away / killed.** | `START_STICKY`: Android restarts it; the lease lapses in at most 60 s meanwhile, and another relay takes over. |
| **Location in the background.** The Wi-Fi name needs the location permission; Android may hide it from a service once the app is closed (no background location is requested). | The relay then reports no Wi-Fi and runs on the box identity alone, and says so. It never fails open on the Wi-Fi: a wrong Wi-Fi name it can read stands it down. |
| **Cost.** | One small HTTPS exchange per 20 s heartbeat plus the long poll (25 s), a device snapshot per minute, SSIDs every 5 minutes. On a charger this is negligible. |

## Manual check on a real Android phone (human step)

Not automated: the service, the camera, the permissions and Doze only exist on
a device. With a debug build pointing at a hub, two phones (owner A, relay B):

1. B: welcome screen → « Être un relais » → « Scanner le QR code »: the camera
   prompt; scan A's QR (A: Réglages → Relais à la maison → Un téléphone qui
   reste à la maison). B shows six digits; A shows the same.
2. Refuse the camera on a second try: the message appears, the code typed
   under A's QR pairs as well.
3. A: « Oui, c'est le même code ». Within 3 s B shows « Relais confirmé ».
4. B: « Démarrer le relais »: notification and location prompts; the ongoing
   notification « Relais Kergui » appears and reads « En service ».
5. From A on mobile data: block then unblock a device; the box applies it.
6. Close the app on B (swipe from recents): the notification stays, A's
   commands still work. Turn the screen off for 10 minutes on the charger, then
   unplugged: note whether commands still pass (Doze).
7. B: « Arrêter » on the notification: it disappears, and A's next command
   says no relay is available (not a 60 s wait).
8. Refuse the location on B and restart: the notification reads « nom du
   Wi-Fi non vérifié » and the relay still works.
9. Move B to another Wi-Fi (a phone hotspot) whose name was not registered:
   « En pause : pas sur le Wi-Fi de la maison »; the box's admin page shows no
   login from B (`kergui` journal on B: « standing down »).
10. Reboot B: the relay service comes back by itself.
11. Turn on alerts on B too: the watch and the relay run side by side with no
    « session expirée » in the journal.
