# Accès partagé aux apps et aux projets

Code : `internal/api/{access,access_members,access_env}.go`, table `resource_members` (migration 0036).

Une app ou un projet a un **propriétaire** (`owner_id`) et, en plus, des **membres** à qui il donne un rôle. Les admins de l'instance gardent un accès total.

## Rôles

| | viewer | contributor | owner (et admin) |
|---|---|---|---|
| état, événements, usage, réseau, logs | oui | oui | oui |
| déployer, redémarrer, arrêter, modifier la config, l'image, les variables, le logo, les sauvegardes | | oui | oui |
| voir le compose, le Dockerfile et les noms des variables | | oui | oui |
| afficher les valeurs des variables | | | oui (tracé dans les événements) |
| supprimer, renommer, publier sur le profil, déplacer | | | oui |
| restaurer une sauvegarde, shell (terminal, SSH, SFTP) | | | oui |
| gérer les membres, transférer la propriété | | | oui |
| changer bind mounts, GPU ou node | | | oui, par la demande approuvée par le superadmin |

Le terminal d'un conteneur de l'admin et l'approbation des bind mounts restent réservés au superadmin.

## Qui hérite de quoi

- Un membre d'un **projet** a le même rôle sur tous ses services, y compris ceux créés plus tard : le rôle est calculé à la lecture, jamais copié.
- On peut aussi donner un rôle sur **un service seul**. Un droit direct peut **élever** un droit hérité (le meilleur des deux gagne), jamais le réduire : il n'y a pas de refus explicite.
- Le propriétaire est unique. On ajoute seulement des comptes existants et actifs.

## Réponses de l'API

- Aucun rôle : **404**, l'app n'existe pas pour cette personne.
- Un rôle trop bas pour l'action : **403**.
- Les listes `/api/apps` et `/api/stacks` contiennent aussi les ressources partagées, avec `role` et, pour un service qui tient son rôle de son projet, `via: "project"`.
- Routes : `GET|PUT|DELETE /api/{apps,stacks}/{id}/members[/{user}]`, `POST …/transfer`, `GET …/env`.
- Chaque action de chaque route est déclarée dans `access.go` (`minRole`). Un test échoue si une route `/apps/{id}` ou `/stacks/{id}` charge sa ressource sans passer par ce contrôle.

## Variables d'environnement et secrets

Les valeurs ne sont **plus renvoyées** par `GET /api/apps/{id}` ni `GET /api/stacks/{id}` : seulement `envKeys`, la liste des noms. Le propriétaire les affiche par `GET …/env`, ce qui laisse un événement.

À l'enregistrement, `env` porte les valeurs à écrire et `keepEnv` les noms laissés tels quels (leur valeur n'est jamais passée par le formulaire). Une variable absente des deux est supprimée. Un mot de passe de registre se remplace, sans se relire.

> Ce masquage protège des fuites accidentelles, pas d'un contributeur malveillant : quelqu'un qui peut déployer une image ou un compose peut lire les variables depuis le conteneur. Ne donnez le rôle contributor qu'à des personnes de confiance.

## Ce qu'un contributeur ne peut pas contourner

- **Bind mounts** : modifier le compose d'un projet pour en ajouter un est refusé, comme pour le propriétaire, tant que la demande n'est pas approuvée ([bind mounts](bind-mount-requests.md)). Les demandes de bind mounts restent réservées au propriétaire et aux admins.
- **GPU** : une app dont la demande de GPU change est refusée à un contributeur.
- **Suspension** : si le propriétaire est suspendu, l'app l'est aussi pour ses membres.

## Transfert et suppression

- Le transfert (`POST …/transfer`) donne l'app ou le projet, et ses services, à un autre compte actif. L'ancien propriétaire ne garde aucun accès. Les demandes de bind mounts en cours de l'ancien propriétaire ne sont pas déplacées.
- Supprimer une app ou un projet supprime ses membres (déclencheurs SQL, quel que soit le chemin de suppression).
- Ajouts, retraits, changements de rôle, transferts et affichages des variables sont inscrits dans les événements de l'app (`app_events.actor_id` garde l'auteur).

## Alertes

Les crashs et suspensions sont envoyés au webhook personnel du propriétaire **et** des contributeurs, pas des viewers.
