Génère un plan structuré pour la demande décrite. Ne jamais écrire de code — uniquement le plan.

Analyse l'impact complet, puis produis le plan dans ce format exact :

---

## Plan : <titre court>

**Contexte** : <situation actuelle et raison de la demande>

**Périmètre**
- Fichiers créés    : <liste ou "aucun">
- Fichiers modifiés : <liste ou "aucun">
- Fichiers supprimés: <liste ou "aucun">
- Composants impactés : <packages Go, reconcilers, workflows CI>
- ADR requis : oui (lien vers `hydra/docs/adr/`) / non

**Étapes**
1. <action concrète et atomique>
2. <action concrète et atomique>
...

**Tests requis**
- Unitaires : <fonctions/reconcilers à couvrir, fake clientsets>
- Intégration : <scénarios envtest/e2e>
- Régression : <ce qui ne doit pas casser — citer les tests existants>
- Sécurité : <si RBAC, auth Hydra, ou crypto touchés>

**Critères d'acceptation**
- [ ] <critère vérifiable objectivement>
- [ ] `make test` passe sans régression
- [ ] `make lint` passe sans avertissement
- [ ] `make build` produit un binaire fonctionnel
- [ ] <critère métier spécifique>

**Risques & compromis**
- <risque identifié avec sa probabilité et son impact>
- <compromis technique assumé>

**Documentation**
- <ADR à créer dans hydra/docs/adr/ / README à mettre à jour / commentaires à ajouter>

---

✅ En attente de validation avant implémentation.
