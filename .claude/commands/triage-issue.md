Génère le ticket de plan manquant à partir d'une GitHub Issue ouverte par un·e contributeur·rice externe, sans être passée par le workflow plan-avant-code habituel. Ne jamais écrire de code — uniquement le plan, et la mise à jour de l'issue pour partager la référence.

Argument attendu (`$ARGUMENTS`) : un numéro d'issue GitHub — que l'issue ait été ouverte sur `hydra-operator` ou sur `hydra`, le ticket `HYDRA-NNN` et son issue miroir vivent toujours côté `hydra` (aucun tracker séparé sur `hydra-operator`, voir sa Partie B). Si le dépôt `hydra` n'est pas attaché à la session, demander à l'utilisateur de l'ajouter avant de continuer.

**Étapes**

1. Lire l'issue indiquée (titre, corps, labels, commentaires) via les outils GitHub disponibles — quel que soit le dépôt où elle a été ouverte.
2. Chercher dans `hydra/docs/specs/implementation-plan.md` si un ticket existant couvre déjà cette demande. Si oui : ne pas en créer un second — relier l'issue au ticket existant (retitrer en `HYDRA-NNN: <titre>` si besoin, commenter avec le lien vers `docs/specs/implementation-plan.md#hydra-nnn`) et s'arrêter là.
3. Sinon, déterminer le prochain numéro de ticket séquentiel (N+1 après le dernier `HYDRA-NNN` du fichier, dépôt `hydra`).
4. Vérifier le comportement réel du code concerné (`hydra-operator` ou `hydra` selon le sujet) avant de rédiger quoi que ce soit — ne jamais supposer, toujours vérifier contre le code.
5. Rédiger le ticket au format `/plan` habituel (Titre, Type, Complexité, Priorité, Description, Fichiers créés/modifiés — en précisant le dépôt concerné, Tests requis, Acceptance criteria, Dépendances), à partir du contenu réel de l'issue.
6. L'ajouter sous l'epic le plus pertinent de `hydra/docs/specs/implementation-plan.md`. Si aucun epic existant ne convient, le signaler explicitement plutôt que d'en forcer un.
7. Mettre à jour l'issue GitHub d'origine (et, si elle a été ouverte sur `hydra-operator`, l'issue miroir créée sur `hydra`) : retitrer en `HYDRA-NNN: <titre>`, ajouter un commentaire liant vers la section du plan, appliquer les labels de priorité/version pertinents.
8. Présenter le plan comme d'habitude et attendre une validation explicite ("ok" / "proceed" / "go" / "valide") avant de committer et pousser la mise à jour de `implementation-plan.md` côté `hydra` — le retitrage/commentaire de l'issue peut se faire immédiatement (c'est du triage, pas de l'implémentation), mais jamais le commit du plan lui-même sans validation.

✅ En attente de validation avant tout commit du plan.
