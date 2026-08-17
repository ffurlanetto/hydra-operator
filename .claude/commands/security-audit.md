Effectue un audit de sécurité ciblé sur le code indiqué ou sur la base de code complète.

Domaines à auditer :

---

### 1. Secrets & Credentials
- Variables d'environnement, `config.example.yaml` : secrets hardcodés ?
- Logs : le JWT de cluster (`internal/tokenstore`), le `registration_token`, ou tout header `Authorization` apparaissent-ils en clair ?
- Git history : `git log -p | grep -iE "(password|secret|token|key)="` ?
- `.gitignore` couvre-t-il `.env*`, `*.key`, `*.pem`, `config.local.*`, un éventuel kubeconfig local ?

### 2. Cryptographie
- Algorithmes utilisés : SHA-256+ pour hash, AES-256 pour chiffrement, RSA-4096+ pour asymétrique ?
- Aléa : `crypto/rand` — jamais `math/rand` pour usage sécurité ?
- Le JWT de cluster est-il persisté uniquement via `internal/tokenstore` (Secret Kubernetes), jamais en clair sur disque ?

### 3. Authentification & Autorisation
- Chaque appel à l'API Hydra (`internal/hydraclient`) porte-t-il le JWT de cluster attendu ?
- Le RBAC (`deploy/base/`, `helm/templates/`) accorde-t-il uniquement les verbes/ressources nécessaires (pas de `*`/`cluster-admin` de confort) ?
- `make rbac-drift-check` passe (Kustomize et Helm accordent le même RBAC) ?
- Le token d'enregistrement (`registration_token`) est-il bien à usage unique côté Hydra, jamais réutilisé côté opérateur après persistance ?

### 4. Validation des entrées
- Toute réponse de l'API Hydra (`desired-state`) est-elle validée avant d'être traduite en objets Kubernetes (pas de confiance aveugle dans un champ) ?
- Pas d'injection possible dans les noms d'objets Kubernetes construits à partir de champs Hydra ?

### 5. Logging & Audit trail
- Événements de reconciliation significatifs loggés (échecs, changements d'état) ?
- PII/secrets absents des logs (niveau `debug` inclus) ?

### 6. Transport & API
- HTTPS forcé pour toute communication avec l'API Hydra en production ?
- `hydra.http_timeout` borné (pas de requête bloquante indéfiniment) ?

### 7. Dépendances & Runtime
- `go list -m all` — CVE critique connues (client-go, controller-runtime, knative.dev/serving en particulier) ?
- Image Docker : tag fixe ou digest ? Base `distroless` à jour, exécution `nonroot` préservée ?
- `runtime_class_name: kata-containers` (ADR-026) toujours positionné par le reconciler de containers, jamais contourné en dehors des scripts de test explicitement documentés comme tels ?

---

**Format du rapport** :

```
## Audit Sécurité — <scope>

### Tableau des vulnérabilités

| Sévérité | Domaine | Description | Fichier:Ligne | Recommandation |
|---|---|---|---|---|
| 🔴 CRITIQUE | | | | |
| 🟠 HAUTE | | | | |
| 🟡 MOYENNE | | | | |
| 🔵 FAIBLE | | | | |

### Résumé
- Critiques : N
- Hautes : N
- Moyennes : N
- Faibles : N

### Actions immédiates (avant tout déploiement)
- [ ] <action>

### Actions recommandées
- [ ] <action>
```
