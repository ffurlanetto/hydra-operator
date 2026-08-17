Effectue une revue de code complète du diff courant (ou des fichiers indiqués). Produis un rapport structuré.

Grilles de vérification à appliquer :

---

### 1. Architecture & Design
- Séparation des responsabilités respectée (logique métier hors `cmd/operator`, dans `internal/reconciler`) ?
- Interfaces explicites entre composants ? Pas de couplage direct entre packages ?
- Pattern Go respecté (errors as values, context propagation, reconcilers idempotents) ?
- Pas de sur-ingénierie (abstraction prématurée, patterns non nécessaires) ?
- Cohérence avec l'architecture existante (ADR-024/025/026 du dépôt `hydra`) ?

### 2. Tests
- Couverture ≥ 80% sur le code nouveau/modifié ?
- Nommage `[Sujet]_[Scénario]_[RésultatAttendu]` ?
- Un test = un comportement ?
- Tests déterministes (pas de dépendance à `time.Now()` non mockable, pas de random) ?
- `make test` passe sans régression ?
- Tests unitaires : fake clientsets (`k8s.io/client-go/kubernetes/fake`, clientsets Knative/OpenShift Route fake) ?
- Changement touchant un reconciler : `make test-envtest` et/ou `make test-e2e` mis à jour si pertinent ?

### 3. Qualité du code
- Pas de `fmt.Println` en production ?
- Erreurs wrappées avec contexte (`fmt.Errorf("ctx: %w", err)`) ?
- Pas d'erreur avalée silencieusement ?
- Nommage clair et cohérent ?
- Pas de TODO sans ticket `HYDRA-NNN` ?
- Conventional Commits sur les messages de commit ?

### 4. Sécurité
- Aucun secret hardcodé (notamment le token de cluster / JWT) ?
- Le RBAC (`deploy/base`, `helm/`) reste minimal et cohérent entre les deux (voir `make rbac-drift-check`) ?
- Auth vérifiée sur chaque appel à l'API Hydra (`internal/hydraclient`) ?
- Pas de CVE dans les nouvelles dépendances (`go list -m all`) ?
- Crypto : algorithmes approuvés (SHA-256+, AES-256, RSA-4096+) ?

### 5. Données & Kubernetes
- Objets Kubernetes construits idempotents (pas de duplication à chaque reconcile) ?
- Pas de PII dans les logs ?
- `runtime_class_name` (ADR-026) et autres champs de sécurité positionnés, jamais contournés silencieusement ?

---

**Format du rapport** :

```
## Revue de code — <scope ou fichiers>

### Synthèse
Score global : 🟢 Approuvé / 🟡 Approuvé avec réserves / 🔴 Bloqué

### Points bloquants 🔴
- <problème> — fichier:ligne — action requise

### Points d'attention 🟡
- <observation> — fichier:ligne — suggestion

### Points positifs 🟢
- <bonne pratique observée>

### Actions requises avant merge
- [ ] <action concrète>
- [ ] <action concrète>
```
