# CLAUDE.md — hydra-operator

---

## Partie A — Noyau commun (immuable)

### Règle fondamentale de workflow

Pour **chaque demande de développement**, sans exception :

1. **Analyser** l'impact complet de la demande
2. **Rédiger un plan structuré** (format ci-dessous) et, si le projet suit ses tickets dans un tracker externe (GitHub Issues, Jira...), **créer ou mettre à jour en parallèle le ticket correspondant**, avec une référence partagée entre le plan et le ticket (conventions exactes en Partie B)
3. **ATTENDRE une validation explicite** ("ok" / "proceed" / "go" / "valide")
4. **Implémenter** étape par étape selon le plan validé
5. **Vérifier** tests + lint + build + zéro régression

**Aucune ligne de code avant validation du plan.**
Toute déviation par rapport au plan en cours d'implémentation doit être signalée et re-validée avant de continuer.

---

### Format de plan obligatoire

```
## Plan : <titre court>

**Contexte** : <pourquoi cette demande, quelle est la situation actuelle>

**Périmètre**
- Fichiers créés   : <liste>
- Fichiers modifiés: <liste>
- Fichiers supprimés: <liste>
- Composants impactés: <liste>
- ADR requis       : oui / non

**Étapes**
1. <action concrète>
2. <action concrète>
...

**Tests requis**
- Unitaires : <ce qui doit être couvert>
- Intégration : <scénarios>
- Régression : <ce qui ne doit pas casser>
- Sécurité : <si applicable>

**Critères d'acceptation**
- [ ] <critère vérifiable>
- [ ] <critère vérifiable>
...

**Risques & compromis**
- <risque ou compromis identifié>

**Documentation**
- <ADR / commentaires / README à mettre à jour>

✅ En attente de validation avant implémentation.
```

---

### Standards universels

**Séparation des responsabilités**
- Logique métier hors des handlers/controllers
- Interfaces explicites entre composants (pas de couplage direct entre packages)
- Chaque package a une responsabilité unique et documentée

**Gestion des erreurs**
- Toujours wrapper avec contexte : `fmt.Errorf("context: %w", err)`
- Jamais avaler silencieusement (`_ = err` interdit sauf cas documenté)
- Errors as values côté Go, erreurs typées côté TypeScript

**Logging**
- Jamais `fmt.Println` / `console.log` en production
- Logging structuré uniquement (zerolog côté Go, pas de console.log côté frontend en prod)
- Jamais de données sensibles dans les logs (tokens, mots de passe, PII)
- Niveau `debug` pour le détail opérationnel, `info` pour les événements métier, `error` pour les anomalies

**Conventional Commits**
```
type(scope): description courte

Types : feat | fix | docs | test | refactor | chore | security | perf
Exemples :
  feat(api): add pagination to users endpoint
  fix(auth): prevent token reuse after logout
  security(deps): update vulnerable dependency
```

---

### Règles tests absolues

- **Zéro régression** tolérée — tous les tests existants passent avant chaque commit
- **Couverture minimale** : 80% unitaire sur le code nouveau/modifié
- **Niveaux requis** :
  - Unitaire sur toute logique métier et handler
  - Intégration sur les flux API critiques
  - E2E sur les workflows critiques (auth, paiement, etc.)
  - Sécurité sur tout endpoint exposé et toute crypto
- **Nommage** : `[Sujet]_[Scénario]_[RésultatAttendu]`
- **Un test = un comportement** — pas de tests multi-assertions non liées
- **Tests déterministes** — pas de `time.Now()` non mockable, pas de random non seedé
- **CI GitHub Actions toujours verte** : après chaque push, vérifier le statut des jobs — tout job rouge doit être diagnostiqué et corrigé avant de considérer la tâche terminée, jamais ignoré, laissé en l'état, ou signalé comme "à corriger plus tard" sans action

---

### Checklist sécurité systématique

Avant tout commit touchant à l'API, la config ou les dépendances :

- [ ] Aucun secret hardcodé (token, password, clé API)
- [ ] Inputs validés côté serveur (jamais seulement côté client)
- [ ] Pas d'interpolation directe dans les requêtes (SQL injection, etc.)
- [ ] Auth + permissions vérifiées sur chaque endpoint
- [ ] Pas de CVE critique dans les dépendances (`go list -m all` + audit npm)
- [ ] Crypto : SHA-256+ / AES-256 / RSA-4096+ uniquement
- [ ] Aléa cryptographique (`crypto/rand`, jamais `math/rand`) pour tout usage sécurité
- [ ] CORS restreint en production (pas `*`)

---

### Documentation & ADR

- Tout changement architectural → ADR **avant** implémentation (`docs/adr/ADR-NNN-titre.md`)
- Commentaires de doc sur tout export public Go (`// FunctionName ...`)
- `TODO` uniquement avec ticket référencé : `// TODO(HYDRA-42): description`
- Voir `/adr` pour créer un ADR, `/plan` pour planifier, `/review` pour réviser

---

### Slash commands disponibles

| Commande | Usage |
|---|---|
| `/plan` | Génère un plan structuré pour la prochaine demande — n'écrit jamais de code |
| `/adr` | Crée un ADR numéroté et met à jour l'index |
| `/review` | Revue de code complète (architecture, tests, sécurité, qualité) |
| `/security-audit` | Audit ciblé sécurité (secrets, crypto, auth, inputs, deps) |
| `/triage-issue` | Génère le ticket de plan manquant à partir d'une issue GitHub ouverte par un·e contributeur·rice externe, et relie les deux — n'implémente jamais |

---

## Partie B — Configuration projet : hydra-operator

### Identité du projet

| Champ | Valeur |
|---|---|
| Nom | hydra-operator |
| Description | Opérateur Kubernetes in-cluster pour [Hydra](https://github.com/ffurlanetto/hydra) — tire l'état désiré depuis l'API du control plane Hydra et le réconcilie en objets Knative `Service`/`DomainMapping` réels ; aucun kubeconfig n'est jamais stocké côté Hydra (ADR-024, ADR-025) |
| Type | Opérateur Kubernetes, binaire Go autonome (pas de serveur HTTP applicatif — seulement `/healthz`, `/readyz`, `/metrics`) |
| Dépôt | github.com/ffurlanetto/hydra-operator |
| Préfixe ticket | `HYDRA-` (partagé avec le dépôt `hydra` — epics et tickets trackés dans `hydra/docs/specs/implementation-plan.md`, pas de tracker séparé ici) |

---

### Stack technique

| Couche | Technologie | Version |
|---|---|---|
| Langage | Go | 1.26 |
| Client Kubernetes | client-go + controller-runtime | k8s.io v0.36.2 / controller-runtime v0.24.1 |
| Client Knative | knative.dev/serving | v0.49.1 |
| Client OpenShift | openshift/api + openshift/client-go | commit épinglé (voir `go.mod`) |
| Logging | zerolog | v1.33.0 |
| Config | Viper | v1.20.1 |
| Tests | testify + race detector + envtest (`sigs.k8s.io/controller-runtime/pkg/envtest`) | — |
| Lint | go vet | — |
| Container | Docker distroless non-root (`gcr.io/distroless/static-debian12`) multi-arch | — |
| CI/CD | GitHub Actions | — |
| Registry | GHCR (ghcr.io/ffurlanetto/hydra-operator) | — |
| Cible de déploiement | Clusters Kubernetes des clients Hydra (Helm ou Kustomize) — ce dépôt n'a pas de cloud/hébergement propre | — |

---

### Commandes de développement

```bash
# Build
make build                 # binaire ./hydra-operator
make run                   # build + lance localement (attend un kubeconfig ou in-cluster)

# Tests
make test                  # go test -race + coverage
make test-envtest          # envtest : vrai kube-apiserver+etcd, pas de controllers réels
make test-e2e              # suite e2e contre $KUBECONFIG (cluster réel avec Knative+Kourier)
make e2e-local              # monte un cluster kind local (+ CRC si présent) et lance test-e2e
make e2e-hydra-integration  # boucle réelle hydra <-> hydra-operator (voir docs/testing/e2e.md, tier 4)

# Lint
make lint                  # go vet

# Docker / Helm / Kustomize
make docker                # image multi-arch amd64+arm64
make helm-lint              # lint du chart Helm
make helm-template          # rendu du chart avec valeurs placeholder
make deploy-validate         # rendu Kustomize (build-only, pas de cluster requis)
make rbac-drift-check        # échoue si le ClusterRole diverge entre deploy/base et helm/

# Utilitaires
make tidy                  # go mod tidy
make clean                 # supprime le binaire et coverage.out
./hydra-operator --version
./hydra-operator --config path/to/config.yaml
```

---

### Architecture du projet

```
hydra-operator/
├── cmd/operator/         # Entrypoint : flags, config, preflight Knative, boucle de reconciliation
├── internal/
│   ├── capabilities/     # Détection des capacités du cluster (Knative, Kourier, OpenShift Routes, Gatekeeper, Kata)
│   ├── config/           # Viper : chargement config.yaml + env vars HYDRA_* + validation
│   ├── hydraclient/      # Client HTTP typé vers l'API control plane Hydra (desired-state, callbacks de statut)
│   ├── k8sclient/        # Construction des clients K8s (in-cluster ou kubeconfig local)
│   ├── logging/          # Factory zerolog
│   ├── reconciler/       # Boucles de réconciliation : namespaces, Knative Service/DomainMapping,
│   │                     #   OpenShift Route, policy Gatekeeper (ADR-026)
│   ├── tokenstore/       # Persistance du JWT de cluster (Secret K8s) après le handshake d'enregistrement
│   └── version/          # Variables ldflags (Version, Commit, BuildDate)
├── test/
│   ├── envtest/          # Tier 2 : vrai kube-apiserver+etcd, pas de controllers réels
│   └── e2e/               # Tier 3 : vrai cluster (kind/k3d/CRC) avec Knative+Kourier réels
├── scripts/
│   ├── e2e-local.sh              # Monte kind + Knative + Kourier localement, lance test-e2e
│   └── e2e-hydra-integration.sh  # Boucle réelle hydra <-> hydra-operator (tier 4)
├── deploy/                # Manifests Kustomize (base + overlays)
├── helm/                  # Chart Helm équivalent
├── docs/testing/e2e.md    # Les 4 tiers de test, en détail
└── .github/workflows/
    ├── build.yml          # go vet + go test + binaire multi-plateforme
    ├── docker.yml         # build Docker → GHCR
    └── e2e-kind.yml       # envtest + e2e kind + e2e hydra-integration (CI)
```

**Modules et responsabilités** :

| Package | Responsabilité |
|---|---|
| `cmd/operator` | Entrypoint uniquement — preflight Knative fatal, câblage des dépendances, pas de logique métier |
| `internal/capabilities` | Détection des capacités du cluster cible |
| `internal/config` | Lecture, validation et exposition de la config |
| `internal/hydraclient` | Client HTTP vers l'API Hydra (desired-state + callbacks de statut) |
| `internal/k8sclient` | Construction des clients Kubernetes/Knative/OpenShift |
| `internal/logging` | Construction du logger selon la config |
| `internal/reconciler` | Toute la logique de réconciliation (namespaces, containers, routes, policy) |
| `internal/tokenstore` | Persistance du JWT de cluster |
| `internal/version` | Constantes de build injectées par ldflags |

---

### Contraintes spécifiques au projet

**Knative uniquement, sans exception (ADR-025)** : `checkKnativeAvailable` (`cmd/operator/main.go`) est un preflight **fatal** — l'opérateur refuse de démarrer si `serving.knative.dev` n'est pas détecté sur le cluster cible, avant même toute tentative d'enregistrement auprès de Hydra. Pas de fallback Deployment/HPA/KEDA.

**Kata Containers, sans exception en production (ADR-026)** : l'API desired-state de Hydra positionne toujours `runtime_class_name: kata-containers` sur chaque container. Un cluster de dev/CI sans Kata réel doit fournir un `RuntimeClass` nommé `kata-containers` (handler `runc` en test uniquement — voir le commentaire dans `scripts/e2e-hydra-integration.sh`) sous peine de rejet des Pods à l'admission.

**Les ADR vivent dans le dépôt `hydra`, pas ici** : ce dépôt n'a pas son propre répertoire `docs/adr/` — toute décision architecturale concernant hydra-operator est documentée dans `hydra/docs/adr/` (ADR-024 à ADR-032 notamment) et référencée depuis le code/README de ce dépôt.

**Limites du sandbox de développement pour les tests e2e** : `kind` échoue d'emblée (bootstrap `kubeadm` imbriqué en Docker-in-Docker), K3s bute sur une race condition non reproductible dans son `runc` embarqué, et `k3d` est bloqué par la politique d'egress du sandbox sur `ghcr.io`. Voir `docs/testing/e2e.md` pour le détail complet — ces trois paliers (`make e2e-local`, `make e2e-hydra-integration`, et `make test-envtest`) ont leur première exécution fiable en CI ou sur une machine réelle, jamais dans ce sandbox.

---

### Standards Go spécifiques

- Reconcilers : signature `func (r *FooReconciler) Reconcile(ctx context.Context, ...) error`
- Erreurs : `fmt.Errorf("Reconcile: %w", err)` — toujours avec contexte
- Tests : fake clientsets (`k8s.io/client-go/kubernetes/fake`, clientsets Knative/OpenShift Route fake) pour le tier 1 ; `envtest` pour le tier 2 ; cluster réel pour le tier 3/4 — voir `docs/testing/e2e.md`
- Pas de `init()` — initialisation explicite dans `main` ou constructeurs (`New...`)

---

### Intégrations externes

| Système | Rôle | Protocole |
|---|---|---|
| API control plane Hydra | Source de l'état désiré (`GET .../desired-state`) + destination des callbacks de statut | HTTPS / JSON REST, polling (`reconciler.sync_interval`, défaut 30s) |
| API Kubernetes (cluster cible) | Cible de toute réconciliation | client-go / controller-runtime, in-cluster ou kubeconfig |
| API Knative Serving | Primitive d'exécution unique (`Service`, `DomainMapping`) | client Go généré `knative.dev/serving` |
| API OpenShift Route | Fallback d'ingress sur OpenShift (pas de Kourier/Gateway API) | client Go généré `openshift/client-go` |
| Gatekeeper (optionnel, auto-détecté) | Application des `ConstraintTemplate`/`Constraint` ADR-026 | client K8s dynamique (`unstructured`) |
| GHCR (ghcr.io) | Registry Docker | HTTPS / Docker registry v2 |
| GitHub Actions | CI/CD | YAML workflows |

---

### Workflow équipe

**Branches** : `main` (release) · feature branches `type/description-courte`

**Commits** : Conventional Commits — `feat(scope): description` — co-authored-by Claude si généré par IA

**Merge** : PR avec review obligatoire · squash merge sur main

**Push & PR automatique** : Toute branche créée pendant une session de développement doit être poussée systématiquement (`git push -u origin <branche>`) — jamais de travail qui reste uniquement local. Dès le premier push d'une branche, une **pull request en draft** doit être créée automatiquement vers `main` (ou la branche cible appropriée), même si le travail n'est pas terminé.

**PR prête automatiquement** : Dès qu'un développement est terminé (tests + lint + build + CI verte, critères d'acceptation du plan remplis), la PR correspondante doit être marquée "ready for review" automatiquement — sans attendre une demande explicite. Une PR en draft alors que le travail est fini est une anomalie à corriger, pas un état d'attente normal.

**Plan ↔ Issue GitHub (référence partagée)** : comme pour les ADR (voir plus bas), les tickets `HYDRA-NNN` et leurs GitHub Issues miroirs vivent côté dépôt `hydra` (`docs/specs/implementation-plan.md`) — pas de tracker séparé ici. Un nouveau ticket dont le travail concerne `hydra-operator` doit quand même avoir son issue miroir créée sur `hydra`, avec le dépôt concerné précisé dans le corps de l'issue (voir la Partie B de `hydra/CLAUDE.md` pour les conventions exactes de titre/corps/labels).

**Ticket externe → plan ad hoc** : Si un·e contributeur·rice externe ouvre une issue sur *ce* dépôt (`hydra-operator`) qui ne correspond à aucun ticket existant, utiliser `/triage-issue <numéro>` — la commande redirige le ticket et son issue miroir vers `hydra` (ajouter ce dépôt à la session si nécessaire) plutôt que de créer un tracker local. Ne jamais implémenter directement depuis une issue externe sans passer par cette étape.

**Environnements** : ce dépôt ne déploie rien lui-même — il produit un binaire/image que les clusters cibles installent (Helm ou Kustomize, voir `deploy/README.md`). Pas d'environnement `staging`/`production` propre à ce dépôt.

**Deploy gate** : `build.yml` doit être vert (vet + tests + binaire multi-plateforme) avant tout merge sur `main`. `e2e-kind.yml` (envtest + e2e kind + e2e hydra-integration) est un signal supplémentaire, gated CI.

**ADR** : requis pour tout changement d'infrastructure, de framework, ou de pattern architectural — mais rédigé et stocké dans `hydra/docs/adr/`, pas dans ce dépôt (voir Contraintes ci-dessus).
