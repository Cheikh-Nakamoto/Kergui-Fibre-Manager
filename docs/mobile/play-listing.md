# Play Console — store listing (French)

Part of the [Play release checklist](release-playstore.md). Copy each block
as is into **Grow users → Store presence → Main store listing**, language
**français (France) – fr-FR** as the default language.

Rules this copy follows (re-check them after any edit):

- **No price, offer, promotion, "gratuit", subscription, payment method, or
  link or hint to pay elsewhere** (RFC-006, decision of 2026-11-21; Play's
  Payments policy forbids steering to a payment outside Play).
- **No operator or manufacturer brand**: never "Orange", "Livebox", "ZTE" or a
  logo, in the title, descriptions, screenshots or graphics (Play's
  impersonation policy). Say « box fibre ».
- No claim the app cannot back ("sécurité totale", "n°1"), no emoji spam, no
  keyword list, no all-caps.

Check the lengths and the forbidden words with:

```bash
python3 - docs/mobile/play-listing.md <<'EOF'
import re, sys
text = open(sys.argv[1], encoding="utf-8").read()
blocks = dict(re.findall(r"<!-- (\w+) -->\n```text\n(.*?)\n```", text, re.S))
for name, limit in (("title", 30), ("short", 80), ("long", 4000), ("notes", 500)):
    n = len(blocks[name]); print(f"{name}: {n}/{limit}", "OK" if n <= limit else "TOO LONG")
bad = re.compile(r"(?i)orange|livebox|zte|prix|tarif|offre|promo|gratuit|abonn|payer|paiement|achat|acheter|fcfa|xof|wave|naboopay|https?://")
for name, body in blocks.items():
    for m in bad.finditer(body): print(f"{name}: forbidden word {m.group(0)!r}")
EOF
```

## Title (30 characters max)

<!-- title -->
```text
Kergui — Gestion box fibre
```

## Short description (80 characters max)

<!-- short -->
```text
Voyez qui est connecté à votre box fibre, bloquez un appareil, gérez le Wi-Fi.
```

## Full description (4000 characters max)

<!-- long -->
```text
Kergui vous aide à reprendre la main sur le réseau de la maison : vous voyez qui est connecté à votre box fibre, vous bloquez un appareil en un geste, et vous êtes prévenu quand un inconnu arrive sur votre Wi-Fi.

L'application parle directement à votre box, sur votre Wi-Fi. Le mot de passe de la box reste chiffré sur votre téléphone, et la liste de vos appareils ne quitte pas votre téléphone.

CE QUE VOUS POUVEZ FAIRE

• Voir tous les appareils connectés : nom, adresses, fabricant et réseau Wi-Fi utilisé.
• Donner un nom clair à chaque appareil (« Téléphone de Awa », « TV du salon »).
• Bloquer un appareil, ou lui couper l'accès au Wi-Fi même s'il connaît le mot de passe, puis le rétablir quand vous voulez.
• Suivre la consommation de chaque appareil sur le mois en cours, et voir son évolution sur plusieurs mois.
• Gérer vos réseaux Wi-Fi : les activer ou les désactiver, les renommer, changer leur mot de passe.
• Recevoir une alerte dès qu'un nouvel appareil se connecte, et l'accepter depuis la notification.
• Activer le rejet automatique : un appareil inconnu que vous n'avez pas accepté est refusé tout seul.
• Consulter le journal technique pour comprendre ce qui ne marche pas, et le partager avec la personne qui vous aide.

ESSAYEZ SANS BOX

Le mode démo lance une box simulée dans l'application : vous découvrez tous les écrans sans rien connecter.

VOTRE COMPTE KERGUI

Vous pouvez relier l'application à un compte Kergui avec votre numéro de téléphone, vérifié par un code reçu sur WhatsApp. Plusieurs téléphones de la famille peuvent utiliser le même compte. Certaines fonctions avancées dépendent du compte associé à votre box.

VOS DONNÉES

• Pas de publicité, pas de pistage.
• Les appareils, leurs adresses et leur consommation restent sur votre téléphone.
• Le compte, facultatif, ne connaît que votre numéro de téléphone, le nom de votre téléphone, le numéro de série de votre box et les droits du compte.

BON À SAVOIR

• Le téléphone doit être connecté au Wi-Fi de la box pour la gérer.
• Il vous faut le mot de passe d'administration de la box (souvent inscrit sur l'étiquette collée dessous).
• Kergui fonctionne avec plusieurs modèles de box fibre courants ; si le vôtre n'est pas encore pris en charge, l'application vous le dit.

Kergui est une application indépendante : elle n'est ni éditée, ni soutenue par votre fournisseur d'accès à Internet ou le fabricant de votre box.
```

## Release notes for the closed test (500 characters max)

<!-- notes -->
```text
Première version de test de Kergui : liste des appareils connectés, blocage et coupure de l'accès Wi-Fi, consommation par appareil, gestion des réseaux Wi-Fi, alertes de nouvel appareil et rejet automatique. Touchez « Essayer la démo » pour tout découvrir sans box. Vos retours sont précieux : décrivez ce qui ne marche pas et joignez le journal technique (Réglages → Journal technique → Partager).
```

## Other listing fields

| Field | Value |
|---|---|
| App category | **Tools** (Outils) |
| Tags | Network tools / Wi-Fi (pick from Play's list) |
| E-mail address (contact) | the contact address of the privacy policy |
| Website | leave empty, or the project page; never the purchase page |
| Privacy policy | `<hub>/licensing/v1/privacy` |

## Graphics

| Asset | Spec | Source |
|---|---|---|
| App icon | 512 × 512 PNG, 32-bit, ≤ 1 MB | from `mobile/assets/icon.png` (same artwork as `internal/webui/assets/logo.png`) |
| Feature graphic | 1024 × 500 PNG/JPEG, no alpha | to draw: the Kergui logo and « Gestion box fibre », no brand, no price, no box photo with a logo |
| Phone screenshots | 2–8, 16:9 or 9:16, each side 320–3840 px | `mobile/.maestro/screenshots.yaml`, see below |

### Screenshots in demo mode

`mobile/.maestro/screenshots.yaml` drives the demo F6600P and writes
`shot-*.png` in the directory maestro is started from (`maestro test
--test-output-dir <dir> mobile/.maestro/screenshots.yaml`; CI collects them
into the `screenshots` artifact, light and dark). The demo box only has
placeholder MACs (`ac:bb:cc:00:00:11`, `…:22`, `…:33`) and names
(Phone-Salon, Laptop-Bureau, TV-Salon).

Upload, in this order (light mode):

1. `shot-2-devices` — « Voyez qui est connecté »
2. `shot-3-device` — « Bloquez ou coupez l'accès Wi-Fi »
3. `shot-4-conso` — « La consommation de chaque appareil »
4. `shot-5-device-usage` — « Son évolution mois par mois »
5. `shot-6-wifi` — « Vos réseaux Wi-Fi »
6. `shot-7-settings` — « Alertes et rejet automatique »
7. `shot-8-account` — optional

**Do not upload `shot-1-welcome`** while the welcome screen says « Gérez
votre box ZTE… » (`welcome.subtitle` in `mobile/src/i18n/fr.ts`): it names
the manufacturer. Captions, if added on the images, follow the same rules as
the copy above.
