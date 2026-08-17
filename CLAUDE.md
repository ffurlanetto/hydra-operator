# CLAUDE.md — hydra-operator

`hydra-operator` is developed alongside [`hydra`](https://github.com/ffurlanetto/hydra) — see that repo's `CLAUDE.md` for the full workflow (plan-before-code, format, standards). This file carries the workflow rules specific to this repo's own git practice.

### Push & PR automatique

Toute branche créée pendant une session de développement doit être poussée systématiquement (`git push -u origin <branche>`) — jamais de travail qui reste uniquement local. Dès le premier push d'une branche, une **pull request en draft** doit être créée automatiquement vers `main` (ou la branche cible appropriée), même si le travail n'est pas terminé — elle peut être marquée "ready for review" une fois complète, sur demande explicite.

### Commits

Conventional Commits — `feat(scope): description` — co-authored-by Claude si généré par IA.

### Merge

PR avec review obligatoire · squash merge sur `main`.
