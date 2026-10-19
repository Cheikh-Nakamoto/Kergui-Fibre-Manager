# Connexion à la box et journal technique

Écrit pour : le propriétaire du projet et quiconque reprend l'app mobile.

Ce document fait deux choses : il liste ce que l'audit du chemin réseau de
l'app a trouvé, et il explique le journal technique ajouté dans l'app pour
que « ça ne marche pas » devienne quelque chose qu'on peut lire.

## Ce que l'audit a trouvé

Classé par ce que le foyer subit, pas par la taille du correctif.

| # | Problème | Ce que ça donnait sur le téléphone | État |
|---|---|---|---|
| 1 | `reach()` abandonnait la recherche dès que l'essai HTTPS échouait après la poignée de main | Une box qui écoute sur 443 mais ne sert pas ses pages là (ou un port 443 occupé par autre chose) rendait la connexion impossible : le repli HTTP, pourtant écrit, n'était jamais tenté. | corrigé |
| 2 | Aucun délai d'attente n'était passé au client HTTP du routeur (`port.RouterOptions{}`) | Le cœur retombait sur le défaut interne de `httpkit` (15 s) ; rien ne le garantissait, et une box injoignable pouvait tenir l'écran jusqu'à 60 s, le temps d'attente d'une requête. | corrigé : 10 s par aller-retour, 30 s pour une requête complète |
| 3 | Un refus du cœur (status 0) ou un dépassement de délai tombaient dans « Erreur : <texte Go> » | Le foyer lisait une phrase en anglais technique au lieu de savoir quoi faire. | corrigé : deux messages distincts (« rouvrez Kergui », « la box n'a pas répondu à temps ») |
| 4 | React Query interrogeait la box toutes les 30 s, y compris app en arrière-plan | Le Wi-Fi était réveillé pour une liste que personne ne regardait, et au retour la liste pouvait rester vieille. | corrigé : `focusManager` branché sur `AppState` |
| 5 | Le refus du certificat par l'utilisateur était traité comme une panne | Annuler à l'écran « Certificat de la box » faisait apparaître « Box injoignable », comme si l'adresse était fausse. | corrigé : un refus n'est pas une erreur |
| 6 | Rien de tout cela ne laissait de trace lisible | Le cœur journalisait vers Logcat (Android) et `os_log` (iOS) : inaccessible au foyer, et perdu dès que l'app se referme. | corrigé : voir le journal, plus bas |

Ce qui reste à faire, et pourquoi ça n'est pas dans ce lot :

- **Savoir si le téléphone est sur le Wi-Fi ou sur la 4G.** L'app dirait
  « vous êtes en données mobiles, la box n'est joignable que sur son Wi-Fi »
  au lieu de « box injoignable ». Cela demande une dépendance de plus
  (`expo-network`) et un choix de formulation ; à traiter avec H3, où l'app
  gagne de toute façon un état « à la maison / à distance ».
- **Réessayer une requête que la box a refusée parce qu'une autre session
  l'occupait** (la box n'accepte qu'une session d'administration). Aujourd'hui
  un 503 est réessayé une fois ; H3 apportera le bail du hub, qui est la vraie
  réponse à ce problème.

## Le journal technique

**Où :** Réglages → Journal technique.

**Ce qu'il montre :** les dernières lignes du cœur, telles que le tableau de
bord de `kergui serve` les affiche déjà — une ligne par appel à l'API avec son
statut et sa durée (`HTTP GET /api/devices -> 200 (120ms)`), les avertissements
des adaptateurs, et les notes de l'app elle-même (l'adresse essayée, un
certificat refusé, un abandon).

**Pourquoi dans le cœur plutôt que dans l'app :** c'est le cœur qui parle à la
box. Un journal tenu côté JavaScript ne verrait ni les sessions perdues, ni les
relectures après écriture, ni la raison exacte d'un refus de la box. Le tampon
circulaire (2 000 lignes, en mémoire) existait déjà dans
`internal/infra/logging` ; `mobilecore` le branche désormais au lieu de ne
journaliser que vers la plateforme.

**Ce qu'il ne contient jamais :** aucun identifiant de la box. Le mot de passe
n'entre pas dans le cœur par le journal et n'en sort nulle part (règle C5 du
projet) ; les notes de l'app sont volontairement limitées à une adresse, un
modèle, un code d'erreur.

**Partager :** un bouton remet les lignes affichées au partage du système
(e-mail, WhatsApp). C'est ce qu'on demandera à un foyer d'envoyer, à la place
d'une capture d'écran de message d'erreur.

### Pour le développeur

- `KerguiCore.journal(since, limit)` renvoie le JSON du tampon, oldest first.
  Il répond **avant** `configure()`, ce qui est tout l'intérêt sur l'écran de
  connexion.
- `KerguiCore.note(level, message)` écrit une ligne de l'app dans le même
  tampon. À utiliser pour ce que seule l'app sait ; jamais pour un secret.
- `GET /api/logs?since=&limit=` est désormais dans la liste des routes que
  l'app peut atteindre (`mobilecore.routes`), pour que l'écran puisse aussi y
  accéder comme n'importe quel autre appel.
- Le niveau arrive tel que le logger l'écrit (`INFO`, `WARN`…) : l'app le met
  en minuscules à l'affichage, comme le tableau de bord le fait déjà.
