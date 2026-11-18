# Plan — Kergui Premium (paiement Wave / Orange Money)

> Plan à appliquer par une prochaine session. Rédigé le 2026-11-18.
> Branche de travail : `ccr-eacfae1c-yic62l`. Ce plan remplace les idées
> précédentes « app payante une fois » et « essai 7 jours puis blocage total ».

## 1. Décisions déjà prises (par le propriétaire)

| # | Décision |
|---|---|
| D1 | L'app est **gratuite** à télécharger. Voir les appareils, les renommer, voir le Wi-Fi : gratuit. |
| D2 | Ce qui **coupe une connexion** est payant (« Kergui Premium ») : bloquer un appareil (liste noire), activer le filtrage MAC, éteindre un réseau Wi-Fi. |
| D3 | Ce qui **rend une connexion** reste gratuit : débloquer, désactiver le filtrage, rallumer un réseau. Un client dont l'abonnement expire ne doit jamais rester bloqué. |
| D4 | Le mode démo garde tout débloqué : il montre ce que fait Premium et sert aux testeurs du Play Store. |
| D5 | Le client doit pouvoir payer par **Wave et Orange Money**. Prestataire envisagé : **NabooPay**. |
| D6 | Tarif d'abonnement : à fixer (ordre de grandeur discuté : 500 à 1 000 F/mois). Essai gratuit de 7 jours possible. |
| D7 | Le F6600P est validé sur la box du propriétaire : **tout fonctionne**, y compris le changement de nom et de mot de passe du Wi-Fi. |

## 2. Le point bloquant : Google Play contre Wave / Orange Money

**Règle Google Play (Payments policy) :** une app distribuée sur Play qui vend
une fonction numérique utilisée dans l'app doit passer par **Google Play
Billing**. Elle ne peut pas encaisser via NabooPay, Wave ou Orange Money, ni
envoyer l'utilisateur vers une page de paiement externe. Les programmes de
facturation alternative (« user choice billing ») n'existent que dans
certains pays : **vérifier si le Sénégal en fait partie**. Probablement pas.
Le non-respect de cette règle peut entraîner le retrait de l'app et la
suspension du compte développeur.

**Deuxième contrainte :** NabooPay demande une **clé secrète** et des
**webhooks**. Il faut donc un petit serveur : on ne met jamais une clé
secrète dans une app.

### Étape 0 — à vérifier avant tout code (par le propriétaire, ~1 h)

1. Play Console → compte marchand : peut-on l'ouvrir depuis le Sénégal ?
2. Quels moyens de paiement Google Play propose-t-il aux acheteurs au Sénégal ?
   Carte, facturation opérateur Orange, mobile money ? (Aide Google Play,
   « moyens de paiement acceptés par pays », ou demander à un ami au Sénégal
   d'ajouter un moyen de paiement dans le Play Store.)
3. Le Sénégal fait-il partie des pays du programme « alternative billing /
   user choice billing » de Google ?
4. NabooPay : créer un compte marchand. Récupérer la doc API (création de
   transaction, page de paiement, webhook, signature du webhook), les frais
   et le délai de versement. **Ne pas inventer d'endpoint : suivre leur doc.**

### Choix de distribution selon les réponses

| Cas | Ce qu'on fait |
|---|---|
| **A.** Google Play accepte Orange Money ou le mobile money au Sénégal | Version Play = **Play Billing seulement** (abonnement `kergui_premium`). Le client paie avec Orange Money **via Google**. NabooPay est inutile pour Play. |
| **B.** Google n'accepte que la carte (cas le plus probable) | **Deux canaux** : 1) Version Play avec Play Billing (pour ceux qui ont une carte) ; 2) **APK direct** sur un site Kergui, avec paiement **NabooPay (Wave / Orange Money)** dans l'app. Même code, deux « flavors » de build. |
| **C.** Alternative billing disponible au Sénégal | Version Play avec le choix Google Play Billing ou NabooPay, via l'API alternative billing de Google. Commission réduite, mais une intégration de plus. |

**Recommandation : construire le code pour le cas B** (un fournisseur de
paiement interchangeable). Le cas A revient alors à désactiver le flavor
NabooPay, et le cas C à brancher NabooPay dans la version Play.

⚠️ **À ne pas faire :** vendre des « codes d'activation » payés sur Wave
hors de l'app, puis les saisir dans la version Play. Google considère cela
comme un contournement de la facturation.

## 3. Modèle commercial avec Wave / Orange Money

Le mobile money n'a pas de prélèvement automatique. Sur le canal NabooPay,
l'« abonnement » est donc un **pass prépayé**, comme une recharge :

| Pass | Prix suggéré | Durée |
|---|---|---|
| Mensuel | 1 000 F | 30 jours |
| Trimestriel | 2 500 F | 90 jours |
| Annuel | 8 000 F | 365 jours |

- Pas de renouvellement automatique. Rappel dans l'app 3 jours avant la fin,
  puis le jour de la fin (notification locale, sans serveur de push).
- Un nouveau pass **s'ajoute** à la date de fin en cours ; on ne perd pas de jours.
- Essai : 7 jours gratuits, **une fois par box**. La clé d'essai est
  l'identifiant de la box (n° de série ou MAC WAN haché), enregistré côté
  serveur. Désinstaller puis réinstaller ne redonne pas d'essai.
- Canal Play : abonnement mensuel Google au même prix (avec un essai de
  7 jours configuré dans la Play Console). Le code de l'app est le même.

## 4. Architecture

```
App (Expo)                          Serveur licences (nouveau, minimal)        NabooPay
 ├ src/billing/gate.ts  (règle D2/D3)
 ├ src/billing/provider.ts (interface)
 │   ├ playProvider.ts (expo-iap) ──── rien (Google est la source de vérité)
 │   └ naboopayProvider.ts ──POST /v1/checkout──► crée la transaction ───────► page de paiement
 │                         ◄──checkout_url────── (Wave / OM)
 │       ouvre checkout_url (navigateur) ......................................► client paie
 │                                            ◄── webhook signé ─────────────── paiement confirmé
 │                                            enregistre, prolonge la date de fin
 │       GET /v1/license?install=… ──────────► renvoie une licence SIGNÉE (Ed25519)
 └ vérifie la signature hors ligne (clé publique embarquée), met en cache
```

### 4.1 Serveur de licences (cas B et C seulement)

- **Où :** nouveau binaire Go `cmd/kergui-license`, dans le style du dépôt
  (Clean Architecture, SQLite, tests). Hébergement petit et peu cher : un VPS
  à 5 €/mois ou Fly.io. Le hub RFC-005 reste en pause : on **ne réutilise
  pas** le hub, trop lourd pour ce besoin.
- **Données :**
  - `installs` : `install_id` (UUID créé par l'app), `box_key` (hash de la box), `created_at`.
  - `trials` : `box_key` UNIQUE, `ends_at`.
  - `payments` : `naboopay_id` UNIQUE, `install_id`, `plan`, `amount_xof`, `status`, `raw_webhook`, `created_at`.
  - `entitlements` : `install_id`, `ends_at`.
- **API :**
  - `POST /v1/install` `{box_key}` → `{install_id}`. Démarre l'essai si `box_key` n'a jamais eu d'essai.
  - `POST /v1/checkout` `{install_id, plan}` → `{checkout_url}`. Appelle l'API NabooPay côté serveur, avec la clé secrète en variable d'environnement.
  - `POST /v1/naboopay/webhook` : vérifie la signature NabooPay, puis est **idempotent** sur `naboopay_id`. Si le paiement est réussi : `ends_at = max(now, ends_at) + durée du plan`.
  - `GET /v1/license?install_id=` → `{install_id, ends_at, trial, sig}` signé en **Ed25519**. La clé privée reste sur le serveur ; la clé publique est embarquée dans l'app.
- **Sécurité :** pas de compte ni de mot de passe utilisateur. HTTPS
  obligatoire. Limite de débit par IP. Aucune donnée de la box n'est envoyée,
  à part le hash `box_key`. La clé secrète NabooPay ne sort jamais du serveur.
- **Tests :** webhook rejoué (idempotence), signature invalide refusée, cumul
  des passes, essai unique par box, licence expirée.

### 4.2 App mobile

Fichiers déjà créés dans cette session (brouillon, **non testés**, à reprendre) :

| Fichier | État | Que faire |
|---|---|---|
| `mobile/package.json` + lock : `expo-iap ~6.4.3` | installé avec npm 11, sans `expo install` (proxy) | garder. Ajouter `"expo-iap"` dans `plugins` de `app.json` |
| `mobile/src/billing/gate.ts` | bon | garder (règle D2/D3/D4) |
| `mobile/src/billing/store.ts` | logique Play Billing complète | déplacer dans `playProvider.ts` derrière l'interface ci-dessous |
| `mobile/src/billing/usePremium.ts` | `guard(action, run)` | garder |
| `mobile/src/app/premium.tsx` | écran Premium (Play) | adapter : liste des passes (NabooPay) ou abonnement (Play) |

Travail restant :

1. **`src/billing/provider.ts`** : interface commune
   `{ init(); entitlement(): {active, endsAt, trial}; offers(); buy(offerId); restore(); manage() }`.
   Le choix du provider se fait au build, par `KERGUI_BILLING=play|naboopay`
   lu dans `app.config.js` → `extra.billing`.
2. **`playProvider.ts`** : le code actuel de `store.ts` (expo-iap, SKU
   `kergui_premium`, `finishTransaction` obligatoire sous 3 jours, cache
   SecureStore).
3. **`naboopayProvider.ts`** :
   - au premier lancement : `install_id` gardé dans SecureStore ; `box_key`
     = sha256 du numéro de série de la box, lu par le core (exposer
     l'info dans `/api/discover` ou `/api/router` s'il ne l'y est pas déjà ;
     voir `router_info`) ;
   - `buy(plan)` → `POST /v1/checkout` → ouvrir `checkout_url` avec
     `expo-web-browser`. Au retour (deep link `kergui://premium/retour` ou
     retour au premier plan), interroger `GET /v1/license` toutes les 3 s
     pendant 2 min ;
   - vérifier la signature Ed25519 dans l'app (`@noble/ed25519`, en JS
     pur), puis mettre en cache la licence signée. Hors ligne, la licence en
     cache fait foi jusqu'à `ends_at`.
4. **Brancher le garde** (`usePremium().guard`) :
   - `src/app/device/[mac].tsx` : `guard('block', …)` seulement quand on
     **bloque** ; débloquer reste libre ;
   - `src/app/(tabs)/wifi.tsx` : `guard('filterOn', …)` quand on active le
     filtrage, `guard('ssidOff', …)` quand on éteint un réseau. Désactiver le
     filtrage, rallumer un réseau, changer le nom ou le mot de passe : libre ;
   - afficher un petit cadenas sur les boutons payants quand Premium est inactif.
5. **`_layout.tsx`** : appeler `useBilling.getState().init()` après `boot()`
   de la session, sans bloquer l'affichage.
6. **Réglages** (`settings.tsx`) : ligne « Kergui Premium — actif jusqu'au
   JJ/MM » ou « Essai : N jours restants », qui ouvre `/premium`.
7. **Rappels d'expiration** : `expo-notifications`, notifications
   **locales** programmées à J-3 et J0 (canal NabooPay seulement).
8. **i18n** (`src/i18n/fr.ts`) : clés `premium.*`. Textes : titre « Kergui
   Premium » ; pitch « Coupez Internet à qui vous voulez » ; fonctions
   « Bloquer un appareil », « Filtrage MAC : n'autoriser que vos
   appareils », « Éteindre un réseau Wi-Fi » ; mention « Voir, renommer et
   débloquer restent gratuits » ; « Payer avec Wave ou Orange Money » ;
   « Paiement en attente de confirmation » ; « Restaurer » ; « Gérer
   l'abonnement » ; mentions légales (renouvellement automatique sur Play,
   pas de renouvellement automatique sur le pass).
9. **Tests Jest** : mocker `expo-iap` dans `jest.setup.ts` ; tester
   `gate.ts` (couper = payant, rendre = gratuit, démo = libre),
   `pickOffer`, le passage acheté → `finishTransaction` → premium, la
   vérification de signature de licence (bonne, fausse, expirée) et le garde
   qui ouvre `/premium`.
10. **CI** (`mobile-release.yml`) : deux artefacts, `.aab` (Play,
    `KERGUI_BILLING=play`) et `.apk` (site, `KERGUI_BILLING=naboopay`),
    signés avec la même clé d'upload ou une clé dédiée pour l'APK. Mettre à
    jour le flow Maestro de démo.

Vérifier avant de finir : `npx tsc --noEmit`, `npx expo lint`, `npx jest --ci`,
`npx expo prebuild --platform android --no-install` (le plugin expo-iap doit
ajouter `com.android.vending.BILLING`), `make check` côté Go.

### 4.3 Core Go

- `internal/adapter/router/ztef6600p/gateway.go:97` : `wifi_ssid_set`
  → `Verified: true`, note « validated live by owner 2026-11 » (D7).
- Si besoin pour `box_key` : exposer le numéro de série ou un identifiant
  stable de la box dans la réponse de discover / router_info, sans rien
  exposer de sensible.

## 5. Documentation à mettre à jour

- `README.md` : remplacer « All endpoints remain UNVERIFIED » par « ZTE
  F6600P : validé sur matériel réel ; autres modèles : UNVERIFIED ».
- `docs/ROADMAP.md` : F6600P validé ; nouvelle section « Monétisation —
  Kergui Premium » qui renvoie vers ce plan.
- `docs/rfcs/RFC-004-standalone-mobile-app.md` : S2 (« vendue une fois, sans
  achat intégré ») devient « gratuite + Premium pour les actions qui
  coupent ». Expliquer la règle D3 et l'exception serveur de licences (S3).
  Idéalement, écrire un **RFC-006 « Kergui Premium »** qui reprend les §2 à 4.
- `docs/rfcs/RFC-005-semi-distributed-hub.md` C7 : noter que le hub est en
  pause et que la monétisation est décrite par RFC-006.
- `docs/mobile/release-playstore.md` §0, §5, §8 : achat intégré par
  abonnement Play (plus « paid app ») ; déclaration Payments ; Data safety
  (canal Play : rien de collecté par nous ; canal APK : `install_id` et
  `box_key` hachés pour la fonctionnalité de l'app) ; §8 distribution APK
  directe pour le flavor NabooPay. Ajouter une politique de confidentialité
  qui mentionne le serveur de licences.
- `docs/mobile/milestones.md` M3 : remplacer « paid app » par la
  monétisation Premium.

## 6. Fiche Play Store (à créer : `docs/mobile/fiche-play-store.md`)

- **Nom (30 car. max)** : « Kergui — Gestion box fibre »
- **Description courte (80 car. max)** : « Voyez qui est sur votre Wi-Fi et
  coupez Internet aux intrus, en un geste. »
- **Description longue** (plan) :
  1. Accroche : « Votre Wi-Fi rame ? Découvrez qui est connecté à votre box fibre. »
  2. Gratuit : liste des appareils connectés (nom, IP, fabricant, Wi-Fi),
     badge « Nouveau », renommer, voir et modifier ses réseaux Wi-Fi.
  3. Premium : bloquer un appareil, filtrage MAC, éteindre un réseau. Essai
     7 jours. Débloquer reste toujours gratuit.
  4. Confidentialité : l'app parle directement à votre box sur votre Wi-Fi ;
     le mot de passe reste chiffré sur le téléphone ; pas de publicité.
  5. Compatibilité : **ZTE F6600P (validé)** ; F660 / F680 en test.
     Le téléphone doit être connecté au Wi-Fi de la box.
  6. Mention obligatoire : « Kergui est une application indépendante, non
     affiliée à Orange ni à ZTE. »
- **Jamais** « Orange », « Livebox » ou « Fiberbox » dans le nom, l'icône ou
  les captures.
- **Captures** (mode démo, aucune vraie MAC) : liste des appareils, détail +
  bouton Bloquer, Wi-Fi, écran Premium. Bannière 1024×500.
- **Catégorie** : Outils. **Public** : 18 ans et plus. **Accès pour les
  testeurs** : « Touchez “Essayer la démo” ».

## 7. Ordre d'exécution pour la prochaine session

1. Étape 0 (propriétaire) → choisir le cas A, B ou C. **Sans cette réponse,
   ne coder que la partie commune** (étapes 2 à 4).
2. Core Go + doc « F6600P validé » (§4.3, début du §5). Petit commit.
3. App : interface provider + playProvider + garde + écran Premium +
   réglages + i18n + tests. Commit.
4. Fiche Play Store + mise à jour de release-playstore / RFC-006. Commit.
5. Cas B ou C : serveur de licences + naboopayProvider + build APK +
   rappels. Commit(s).
6. Tests en interne : abonnement de test Play (comptes testeurs de
   licence) ; NabooPay en mode sandbox ; vrai paiement Wave de 100 F, puis
   remboursement.

## 8. Risques

| Risque | Parade |
|---|---|
| Retrait du Play Store pour paiement externe | NabooPay uniquement dans l'APK direct (cas B) ou via alternative billing officielle (cas C) |
| APK direct : confiance, alerte « source inconnue », vérification développeur Android en 2027 | site HTTPS simple, APK signé, compte développeur vérifié |
| Webhook NabooPay perdu | bouton « J'ai payé » qui force `GET /v1/license` ; le serveur interroge NabooPay au besoin |
| Fraude (APK modifié qui saute le garde) | accepté : le montant est faible, le code est source-available (licence BSL) |
| Firmware Orange mis à jour qui casse l'app | journal technique + mise à jour rapide ; Premium ne rembourse pas automatiquement |
| Coût serveur | un VPS minimal ; il reste rentable dès quelques abonnés |
