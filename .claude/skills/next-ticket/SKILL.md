---
name: next-ticket
description: Traite UN seul ticket GitHub (issue `ready-for-agent`) avec UN seul sous-agent, puis commit, push et ferme l'issue. Conçu pour tourner en boucle avec `/loop /next-ticket`.
---

# Règle de loop : un ticket, un agent, à la fois

Chaque appel de ce skill = une itération = **exactement un ticket** traité par **exactement un sous-agent**.
Ne jamais lancer deux agents en parallèle, ni en arrière-plan. Attendre le résultat de l'agent avant toute autre action.

## 1. Choisir le ticket

```bash
gh issue list --label ready-for-agent --state open --limit 100 --json number,title,labels,body
```

Un ticket est **éligible** si :
- il a une section `## Parent` (le PRD parent lui-même, ex. #21, n'est jamais traité) ;
- il n'a **pas** le label `v2` (reporté à la V2) ni `blocked` ;
- toutes les issues listées sous `## Blocked by` sont **fermées** (`gh issue view N --json state`).

Prendre d'abord les tickets éligibles avec le label `priority`, puis les autres ; à égalité, le **plus petit numéro**.

Règle produit V1 (décision du 2026-11-21) : l'app Play n'a **aucun** paiement (ni écran, ni prix, ni lien, ni texte qui renvoie vers un paiement). Les passes et l'essai se prennent sur la page web du hub. Rappeler cette règle à l'agent pour tout ticket qui touche au mobile.
S'il n'y en a aucun : arrêter la loop (`ScheduleWakeup` avec `stop: true`) et résumer ce qui a été fait et ce qui reste bloqué (et par quoi).

## 2. Préparer

- `git status` doit être propre et la branche à jour (`git pull --ff-only`). Sinon : arrêter la loop et le signaler.
- Lire le ticket complet : `gh issue view N --comments`.

## 3. Lancer UN sous-agent (premier plan)

`Agent` avec `subagent_type: general-purpose`, **sans** `run_in_background`. Le prompt contient le titre, le corps et les commentaires du ticket, plus :

> Tu implémentes l'issue #N du dépôt Kergui-Fibre-Manager, et rien d'autre.
> 1. Vérifie d'abord dans `git log` et le code si c'est déjà fait (en tout ou en partie). Ne refais pas ce qui existe, complète seulement ce qui manque.
> 2. Lis les docs liées (`docs/rfcs/RFC-006-*.md`, `mobile/AGENTS.md` si tu touches au mobile).
> 3. Implémente en TDD : tests d'abord, sur le routeur mock et une horloge factice quand c'est pertinent.
> 3b. Mobile : ajoute un fichier `mobile/.maestro/<feature>-steps.yaml` pour la nouvelle fonctionnalité (sans clearState), branché dans `demo.yaml` et `demo-device.yaml`, et donne son nom dans ton rapport.
> 4. Vérifie, selon ce que tu as touché : `make check` (Go), `make hub-test` (hub), `cd mobile && npm run typecheck && npm run lint && npm test` (mobile). Tout doit être vert.
> 5. Ne commit pas, ne push pas, ne ferme pas l'issue : l'orchestrateur s'en charge.
> 6. Termine par un rapport : critères d'acceptation cochés ou non (avec la raison), fichiers modifiés, commandes de test lancées et leur résultat, et ce qui demande une action humaine.

## 4. Vérifier et livrer

**Tous les tests sont locaux.** La CI GitHub ne tourne que sur un push de tag (`hub-v*`, `mobile-v*`, `v*`) : ne jamais attendre ni invoquer la CI pour valider un ticket.

- Relancer soi-même les commandes de test concernées :
  - Go : `make check` ; hub : `make hub-test` (Postgres docker).
  - Mobile : `cd mobile && npm run typecheck && npm run lint && npm test && bash scripts/check-play-bundle.sh`.
  - Si le ticket change l'interface mobile et qu'un téléphone est listé par `adb devices` : `bash scripts/mobile/local-e2e.sh --flow <étapes-du-ticket>.yaml` (build, installation, puis **seulement** les étapes Maestro du ticket, après l'ouverture de la démo ; l'utilisateur ne veut pas revoir à chaque passage les tests déjà faits). Le flow complet (`local-e2e.sh` sans `--flow`) ne se lance que sur demande ou avant un tag. Quand Réglages s'allonge, un flow qui tape sur une ligne doit d'abord `scrollUntilVisible`. Sans téléphone, le noter dans le commentaire de l'issue au lieu de bloquer. Ne jamais passer `--reinstall` (il efface les données de l'app sur le téléphone) sans l'accord explicite de l'utilisateur. Si un test échoue ou qu'un critère d'acceptation n'est pas rempli :
  ne pas commit, commenter l'issue (`gh issue comment N`) avec ce qui manque, ajouter le label `blocked`, **arrêter la loop** et le signaler.
- Si un critère demande une action humaine (console Play, compte NabooPay, secrets…), faire le reste, commit, laisser l'issue **ouverte** avec un commentaire qui liste les étapes humaines et le label `blocked`, puis continuer la loop.
- Sinon :
  1. Commit en Conventional Commits (`feat(scope): …`), avec `Closes #N` dans le corps.
  2. `git push`.
  3. `gh issue close N --comment "Implémenté dans <sha>."` (si le push ne l'a pas déjà fermée).

## 5. Itération suivante

Résumer en 2–3 lignes (ticket, SHA, tests), puis programmer l'itération suivante :
`ScheduleWakeup` avec `delaySeconds: 60`, `prompt: "/next-ticket"`, `noop: false`.
