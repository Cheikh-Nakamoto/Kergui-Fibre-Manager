# Licence, en clair

Écrit pour : les personnes qui utilisent Kergui chez elles, celles qui veulent
contribuer, et les entreprises qui envisagent de s'en servir.

Kergui Fibre Manager est publié sous **Business Source License 1.1** (BSL
1.1). Le texte qui fait foi est [`LICENSE`](../LICENSE) ; cette page l'explique
sans jargon.

## Si vous l'utilisez chez vous

C'est gratuit, sans limite de durée, sans compte à créer et sans demander la
permission. Gérer le Wi-Fi de votre maison, celui de votre famille, celui de
votre association ou de votre entreprise **pour vos propres besoins** est
couvert par la licence.

Vous pouvez aussi lire le code, le modifier, le compiler, en faire des copies,
l'utiliser pour apprendre, enseigner, tester ou faire de la recherche.

## Si vous voulez en faire une activité payante

Il faut un accord avec l'auteur. Cela concerne, par exemple :

- revendre Kergui, ou le vendre sous un autre nom ;
- l'héberger pour des clients (un hub mutualisé, un service en ligne) ;
- l'inclure dans une offre payante d'accès à Internet, de gestion de réseau ou
  de support ;
- l'utiliser comme outil de travail d'une offre de ce genre.

Dans ces cas-là, [ouvrez une
issue](https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/issues) pour
entrer en contact. Les conditions sont discutées au cas par cas : cela peut
être un montant fixe, ou une part du chiffre d'affaires que Kergui aide à
générer. Rien n'est automatique, et rien n'est caché : le but est que l'auteur
soit associé à la valeur créée, pas d'empêcher qui que ce soit de travailler.

## Pourquoi ce choix

Trois raisons :

1. **Le code doit rester lisible par tout le monde.** Kergui parle à la box
   d'un foyer et manipule son mot de passe ; personne ne devrait avoir à nous
   croire sur parole. La BSL laisse le code public et auditable.
2. **Un foyer ne doit jamais payer pour se protéger.** L'usage domestique est
   gratuit, et le restera : c'est le cœur du projet.
3. **Le travail doit pouvoir être soutenu.** Si une entreprise construit une
   offre payante sur ce code, elle en discute avec l'auteur. C'est la
   différence avec MIT, sous laquelle le projet a commencé : MIT permettait de
   tout reprendre et de tout revendre sans rien dire.

## Et dans quatre ans

La BSL n'est pas une licence open source, mais elle le devient. Chaque version
publiée passe automatiquement sous **Apache-2.0** quatre ans après sa sortie
(`Change Date` dans la licence). Passé ce délai, cette version est libre au
sens habituel, usage commercial compris, sans rien demander à personne.

## Les versions déjà publiées sous MIT

Le projet a commencé sous licence MIT. Les commits publiés avant le
2026-10-19 restent disponibles sous MIT : une licence déjà accordée ne se
retire pas. Le texte est conservé dans [`LICENSE-MIT`](../LICENSE-MIT).

## Si vous contribuez

En proposant une contribution, vous acceptez qu'elle soit publiée sous la
licence du projet, y compris la bascule vers Apache-2.0 à la `Change Date`, et
que l'auteur puisse accorder des licences commerciales couvrant l'ensemble du
projet.

## Composants tiers

Kergui dépend de bibliothèques qui gardent **leur** licence, pas celle-ci. Deux
méritent une mention particulière :

- le noyau [agile-factory/kernel](https://github.com/agile-factory/kernel),
  utilisé par le hub ;
- [Evolution Go](https://github.com/EvolutionAPI/evolution-go), le fournisseur
  WhatsApp du pilote : sa licence (Apache-2.0 avec clauses de marque) impose
  d'afficher une mention visible indiquant qu'il est utilisé, et son instance
  doit être enregistrée avant de pouvoir envoyer un message. Le hub le fait
  (voir RFC-005, « H2 as built »).

Rien dans cette page ne vous dispense de respecter ces licences.
