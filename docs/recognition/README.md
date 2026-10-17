# Reconnaissance des appareils — ZTE F6600P

Ce dossier documente l'audit « identification des appareils Wi-Fi » et la
reconnaissance qui en découle. Il regroupe trois livrables du cahier des
charges :

- **Livrable 5** — [scénarios de changement de MAC et d'IP](#livrable-5--scénarios-de-changement-de-mac-et-dip)
- **Livrable 6** — [algorithme de rapprochement et ses limites](#livrable-6--algorithme-de-rapprochement)
- **Livrable 8** — [recommandations classées](#livrable-8--recommandations-classées)

Les livrables 1–4, 7 et 9 (inventaire des sources, requêtes testées, formats,
matrice de disponibilité, tests automatisés, données impossibles à obtenir) sont
couverts par le code et les fixtures :

| Livrable | Où |
|---|---|
| 1. Inventaire des sources | section [Sources retenues](#sources-retenues) ci-dessous |
| 2–3. Requêtes et endpoints testés | [`observe.go`](../../internal/adapter/router/ztef6600p/observe.go) (constantes + commentaires `observé 2026-10-17`) |
| 4. Matrice de disponibilité des champs | `TestLive_ObserveFieldAvailability` dans [`live_test.go`](../../internal/adapter/router/ztef6600p/live_test.go) — produit la matrice pour n'importe quel firmware |
| 7. Tests sur réponses capturées et anonymisées | [`observe_test.go`](../../internal/adapter/router/ztef6600p/observe_test.go), fixtures `internal/fixtures/zte_f6600p/*` |
| 9. Données inaccessibles | section [Hors de portée](#hors-de-portée) |

> **Principe.** La reconnaissance sert la gestion du réseau par le propriétaire
> autorisé du routeur (inventaire, blocage, facturation équitable). Elle ne lie
> jamais deux adresses automatiquement sans preuve certaine ; tout rapprochement
> incertain passe par l'administrateur. Aucune collecte au-delà de ce que
> l'interface d'administration du routeur affiche déjà.

---

## Contexte mesuré (2026-10-17, firmware `ZTEGF660006SN`, compte `user`)

- **Session unique.** Chaque connexion déconnecte la précédente. L'app, le
  serveur `kergui serve` et le navigateur s'évincent mutuellement. La collecte
  doit être espacée et tolérer une reconnexion.
- **Préchargement de vue obligatoire.** Une page de données ne répond que si sa
  `menuView` a été chargée juste avant, dans la même session ; sinon le routeur
  renvoie `SessionTimeout`. C'est reproduit par le faux routeur des tests.
- **La table DHCP garde les partis.** Les baux restent listés jusqu'à leur
  expiration (bail observé ≤ 24 h). Un relevé quotidien voit donc tous les
  appareils passés dans la journée, sous réserve d'un redémarrage du routeur.
- **Pas de serveur H24 requis.** Le modèle tient avec des relevés ponctuels
  (ouverture de l'app, tâche de métrage existante). Rien n'exige une passerelle
  de surveillance allumée en permanence.

### Sources retenues

| Source (page → tag) | Champs utiles | Rôle |
|---|---|---|
| `localNetStatus` → `wlan_client_stat_lua.lua` | MAC, IP, IPv6 LL, nom, SSID (AP), `LinkTime` | appareils associés **maintenant** |
| `lanMgrIpv4` → `Localnet_LanMgrIpv4_DHCPHostInfo_lua.lua` | MAC, IP, nom, port SSID/LAN, `ExpiredTime`, n° de bail | baux (y compris appareils partis) |
| `mmTopology` → `topo_lua.lua` (JSON) | norme 802.11, bande, sécurité, 802.11k/v | capacités radio |
| `homePage` → `accessdev_data` (`&DeveiceType=ALL`) | MAC, IP, nom | liste consolidée (vue d'ensemble) |
| `statusMgr` → `devmgr_statusmgr_lua.lua` | modèle, firmware, `PowerOnTime` | contexte (détection de reboot) |

Disponibilité mesurée sur un relevé réel (21 stations : 14 associées,
7 connues seulement par leur bail) :

```
associated   14/21     hostname   17/21     dhcp_lease  21/21
random_mac    8/21     ssid       21/21     radio_caps  14/21
ipv4         21/21     band       14/21     assoc_since 14/21
```

### Hors de portée (données impossibles dans l'architecture actuelle)

- **Options DHCP 55/60/61 (Parameter Request List, Vendor Class, Client-ID)** :
  jamais exposées par l'interface. Elles seraient le signal de rapprochement le
  plus stable, mais exigeraient une capture réseau sur la passerelle elle-même.
- **DHCPv6 DUID** : la table existe mais reste vide — le routeur n'annonce qu'en
  SLAAC (flag M de RA à 0). Donc pas de DUID exploitable.
- **Journaux système / DHCP / Wi-Fi** : aucune page de journal dans le menu du
  compte `user`.
- **Durée de bail** : non lisible par `user` (supposée 24 h dans le code).
- **TR-064 / TR-069 / SNMP** : non ouverts sur le LAN. UPnP est présent mais
  renvoie `899 Unsupport PCP Opcode` et n'expose rien d'utile.
- **Probe requests / RSSI de scan passif** : exigeraient un mode moniteur
  Wi-Fi, non disponible et intrusif. Écarté.

---

## Livrable 5 — scénarios de changement de MAC et d'IP

Pour chaque scénario : ce qui **persiste**, ce qui **change**, et si un
rapprochement **fiable sans le propriétaire** est possible. Chaque ligne est
couverte par un test dans
[`recognition_test.go`](../../internal/domain/recognition_test.go).

| # | Scénario | Persiste | Change | Rapprochement auto fiable ? | Décision | Test |
|---|---|---|---|---|---|---|
| 1 | Même appareil, même MAC, nouvelle IP | MAC | IP | **Oui** | `Associated` | `TestScenario1` |
| 2 | Nouvelle MAC, même IP (bail réattribué) | IP, nom | MAC | Non — proposé | `Proposed` | `TestScenario2` |
| 3 | Nouvelle MAC et nouvelle IP, nom conservé | nom | MAC, IP | Non — proposé | `Proposed` | `TestScenario3` |
| 4 | Oubli puis reconnexion Wi-Fi (MAC re-randomisée) | nom | MAC | Non — proposé | `Proposed` | `TestScenario4` |
| 5 | Redémarrage du téléphone + renouvellement DHCP | MAC (persistante par réseau), nom | IP, bail | **Oui** | `Associated` | `TestScenario5` |
| 6 | Absent plusieurs heures/jours, retour nouvelle MAC | nom | MAC, IP | Non — simple indice | `Unassigned` (indice) | `TestScenario6` |
| 7 | Deux appareils même marque/modèle, noms proches | — | — | Non — jamais lié | `Unassigned` (0 candidat) | `TestScenario7` |
| 7b | Deux appareils au nom générique « iPhone » | — | — | Non — nom non discriminant | `Unassigned` (0 candidat) | `TestScenario7b` |
| 7c | Nom partagé par deux appareils vus ensemble | — | — | Non — nom neutralisé | `Unassigned` | `TestScenario7c` |
| 8 | Métadonnées identiques, vus en ligne ensemble | — | — | Non — veto co-présence | `Unassigned` (0 candidat) | `TestScenario8` |
| 8c | Nouvelle MAC, radio contradictoire même bande | — | — | Non — veto radio | `Unassigned` | `TestScenario8c` |
| 9 | Changement de bande / point d'accès | MAC | bande, radio | **Oui** | `Associated` | `TestScenario9` |
| 10 | IPv6 temporaires | MAC | IPv6 | **Oui** (IPv6 ignoré) | `Associated` | `TestScenario10` |

**Lecture.** Le seul rapprochement automatique fiable est la **MAC identique**
(scénarios 1, 5, 9, 10). C'est possible plus souvent qu'il n'y paraît : Android
garde par défaut une MAC privée **persistante par réseau**, stable au
redémarrage et même après « oublier puis rajouter » le réseau ; iOS 18 fait de
même en mode « Adresse fixe » (défaut sur un réseau WPA2+). La re-randomisation
(scénarios 2–4) reste l'exception — option développeur, réseau ouvert, ou
rotation iOS toutes les deux semaines. Quand la MAC change, aucun signal du
routeur ne permet de relier avec certitude : le rapprochement passe alors par le
propriétaire (voir livrable 6).

> Sources comportement MAC :
> [Android](https://source.android.com/docs/core/connect/wifi-mac-randomization-behavior),
> [Apple](https://support.apple.com/en-us/102509).

---

## Livrable 6 — algorithme de rapprochement

Code : [`recognition.go`](../../internal/domain/recognition.go),
[`observation.go`](../../internal/domain/observation.go).

### Identité permanente

Chaque appareil physique reçoit un identifiant interne permanent
`DEVICE-000042` (`domain.DeviceID`) qui ne change jamais quand la MAC, l'IP ou
le nom changent. Une fusion confirmée par l'administrateur conserve l'identité
survivante et enregistre l'autre comme alias. L'historique rattaché à une
identité est une liste d'`Observation` (MAC, horodatage, IP, nom, SSID, bande,
bail DHCP, capacités radio) — uniquement ce que l'interface du routeur affiche.

### Preuves, forces, et trois issues

Le rapprochement repose sur des **preuves** classées par **force ordinale**
(jamais une probabilité) :

- **Décisive** — tranche seule. Ici : **MAC identique** (lien) ; **co-présence**
  et **contradiction radio** (séparation).
- **Corroborante** — soutient une proposition, jamais un lien automatique.
  Ici : **même nom d'hôte discriminant** + **handoff** (nouvelle MAC vue juste
  après qu'un appareil a disparu).
- **Descriptive** — affichée, ne décide rien (ex. « random → universal »).

Trois issues, conformes au cahier des charges :

1. **`Associated`** — preuve décisive positive (MAC identique) : rattachement
   automatique.
2. **`Proposed`** — nom discriminant **et** handoff : proposition à
   l'administrateur, qui confirme ou refuse. Classée par appareil vu le plus
   récemment.
3. **`Unassigned`** — sinon : nouvelle observation non attribuée. Un nom seul
   est conservé comme simple **indice** que l'administrateur peut exploiter à la
   main ; un handoff seul (sans nom) n'est rien.

### Garde-fous (ce qui n'est jamais fait)

- **Jamais** de fusion sur le seul nom d'hôte, la marque, le modèle ou un
  comportement réseau similaire. Un nom seul ne donne qu'un indice.
- **Noms non discriminants ignorés** : `iPhone`, `android-xxxx`, vide, un nom
  ressemblant à une MAC (`domain.IdentityKey`).
- **Veto co-présence** : si la MAC candidate et une MAC de l'appareil connu ont
  été associées **en même temps** (au-delà d'un seuil, `MinOverlap = 2 min`),
  ce sont deux appareils distincts — le candidat est rejeté, quel que soit le
  nom. Un bref chevauchement (roaming *make-before-break*) ne déclenche pas le
  veto.
- **Veto radio** : sur une même bande, un chipset ne change pas de norme 802.11
  ni de support 802.11k/v ; une contradiction sépare deux appareils.
- **Nom partagé** (`sharedNames`) : un nom vu simultanément sur deux appareils
  est définitivement neutralisé et ne compte plus jamais.
- **Aucun score global.** Les seuils sont des **durées** (`RecognitionRules`:
  `HandoffWindow = 15 min`, `MinOverlap = 2 min`, `SamePoll = 5 s`), pas des
  poids additionnés qu'on prendrait pour une probabilité.

### Limites assumées

- **Sans la MAC, pas de certitude.** Les seuls identifiants vraiment stables
  (options DHCP, DUID) ne sont pas exposés par ce firmware. Toute re-association
  sous nouvelle MAC reste une **proposition**, pas un fait.
- **Faux rapprochements possibles** si l'administrateur confirme trop vite une
  proposition fondée sur nom + handoff — deux personnes du même foyer qui
  éteignent/allument à la suite. D'où la confirmation humaine obligatoire.
- **Le nom d'hôte est contrôlé par l'appareil** : il peut mentir, être absent
  (17/21 seulement) ou changer. Il n'est jamais décisif.
- **Capacités radio peu discriminantes** : partagées par tous les exemplaires
  d'un modèle. Utilisées seulement pour *séparer*, jamais pour *lier*.
- **Horloge du routeur** : `ConnectTime` est à 1970 tant que SNTP n'a pas
  synchronisé ; le code s'appuie sur `LinkTime` (durée relative), plus robuste.

---

## Livrable 8 — recommandations classées

Classées par **fiabilité** (du rapprochement obtenu), avec coût de collecte et
complexité d'implémentation.

| # | Recommandation | Fiabilité | Coût collecte | Complexité | Statut |
|---|---|---|---|---|---|
| R1 | **Clé = MAC identique** (rattachement auto) | Très haute | Nul (déjà lu) | Faible | **Fait** (`recognition.go`) |
| R2 | **Relevé DHCP quotidien** pour capter les appareils partis | Haute | Faible (1 relevé/j) | Faible | **Fait** (`Observe`) |
| R3 | **Déclaration par le propriétaire** via portail captif / inscription membre quand la MAC change | Très haute | Faible | Moyenne | Recommandé (hameçon `[[portal]]`) |
| R4 | **Proposition nom + handoff** validée par l'admin | Moyenne | Nul | Moyenne | **Fait** (`Proposed`) |
| R5 | **Veto co-présence + radio** contre les faux positifs | Haute (évite les erreurs) | Nul | Moyenne | **Fait** |
| R6 | **Revoir la blacklist** qui pénalise aujourd'hui l'usage d'une MAC privée légitime | — (correctif privacy) | Nul | Faible | À faire (`internal/usecase/identity.go`) |
| R7 | **Détecter le reboot routeur** (`PowerOnTime`) pour ne pas confondre fin de bail et départ | Moyenne | Nul | Faible | Partiel (champ lu) |
| R8 | Inciter aux **MAC persistantes** (défaut Android/iOS) côté utilisateur | Haute | Nul | Faible (doc) | Recommandé |
| R9 | Capture réseau sur la passerelle (options DHCP 55/60/61) | Très haute | **Élevé** (serveur H24, intrusif) | Élevée | **Écarté** (hors contrainte) |
| R10 | Fingerprinting passif / probe requests | Variable | Élevé (mode moniteur) | Élevée | **Écarté** (intrusif, vie privée) |

**Priorités.** R1+R2 sont la base déjà en place. R3 (déclaration volontaire) est
le meilleur rapport fiabilité/respect de la vie privée pour les cas de
changement de MAC et devrait précéder tout raffinement d'heuristique. R6 est un
correctif à traiter : la logique d'identité actuelle sanctionne un comportement
par défaut et légitime des téléphones. R9 et R10 sont écartés : ils violent soit
la contrainte « pas de serveur H24 », soit l'interdiction d'identification
cachée.

---

## Intégration restante

Le port `port.HostObserver` et `domain.Recognise` existent et sont testés, mais
ne sont **pas encore branchés** sur un use case ni persistés. Les étapes
suivantes (hors de cet audit) : un `IdentityRegistry` qui appelle `Recognise` à
chaque relevé, une table d'historique des observations, et l'écran admin de
confirmation des propositions.
