Crée un nouvel ADR pour la décision architecturale décrite.

ADR-NNN vit dans le dépôt `hydra` (`github.com/ffurlanetto/hydra`), pas dans ce dépôt — hydra-operator n'a pas son propre répertoire `docs/adr/` (voir CLAUDE.md, "Contraintes spécifiques au projet"). Si le dépôt `hydra` n'est pas déjà présent dans la session, l'attacher d'abord.

**Étapes** :
1. Dans le dépôt `hydra`, lire `docs/adr/README.md` pour déterminer le prochain numéro séquentiel (N+1 après le dernier de l'index)
2. Créer `docs/adr/ADR-NNN-titre-en-kebab-case.md` avec le template ci-dessous
3. Mettre à jour la table d'index dans `docs/adr/README.md` avec la nouvelle entrée (statut : 🟡 Proposé)
4. Référencer l'ADR depuis le code/README de hydra-operator concerné (comme le fait déjà `deploy/README.md` pour ADR-024/025)

**Template ADR** :

```markdown
# ADR-NNN — <Titre court et descriptif>

**Statut** : 🟡 Proposé
**Date** : YYYY-MM-DD
**Décideurs** : <noms ou rôles>
**Tags** : <infrastructure | framework | pattern | sécurité | perf>

---

## Contexte

<Situation actuelle, problème rencontré ou opportunité identifiée. Neutral — pas de jugement.>

## Décision

<La décision prise, formulée clairement en une ou deux phrases.>

## Justification

<Pourquoi cette décision ? Critères de choix, données quantitatives si disponibles.>

## Conséquences positives

- <bénéfice concret>
- <bénéfice concret>

## Conséquences négatives / risques

- <coût ou risque assumé>
- <coût ou risque assumé>

## Alternatives considérées

| Alternative | Raison du rejet |
|---|---|
| <option A> | <raison> |
| <option B> | <raison> |

## Mise en œuvre

<Étapes concrètes pour implémenter cette décision, ou lien vers le PR/issue.>

## Références

- <lien documentation, benchmark, ticket>
```
