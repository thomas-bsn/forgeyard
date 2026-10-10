# Support

L'onglet **Support** permet à chaque membre d'écrire aux admins de l'instance, et aux admins de répondre dans le même fil.

Code : `internal/api/support.go`, `web/src/Support.tsx`.

## Une demande

- **Type** : Général (une question, une idée), **Une app** (choisie parmi les siennes ; son nom reste affiché si elle est supprimée) ou **Infra** (un node, le réseau, le DNS).
- **Sujet** (3 à 120 caractères) et **message** (5000 caractères max.), puis des réponses.
- Un membre voit ses demandes ; les admins les voient toutes et répondent. L'auteur et les admins peuvent la fermer ; un nouveau message la rouvre.
- La liste se filtre par Ouvertes / Fermées / Toutes. L'onglet porte une pastille : pour un admin, les demandes dont l'auteur a écrit en dernier (une réponse est attendue) ; pour un membre, celles qui ont reçu une réponse.

## Notifications

- Nouvelle demande, ou nouveau message de son auteur : **salon support** des admins (Réglages › Notifications) s'il est renseigné, sinon le salon des admins. Le type d'événement « Support » s'active à part.
- Réponse d'un admin : le **webhook personnel** de l'auteur (Mon profil › Notifications), s'il en a un.

Le salon support reçoit aussi les **demandes de compte** : tout ce que les membres demandent aux admins arrive au même endroit, séparé des alertes (crashs, nodes).

## Données

`support_tickets` (auteur, type, app, sujet, statut, `waiting` : l'auteur a écrit en dernier) et `support_messages` (fil). Supprimer un compte supprime ses demandes ; ses messages dans les demandes des autres restent, signés « Compte supprimé ».
