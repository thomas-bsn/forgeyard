# Routes de l'API

Toutes les écritures sont en JSON. Les routes sont déclarées dans `internal/api/router.go`.

## Publiques

| Route | Rôle |
|---|---|
| `GET /api/health` | Santé |
| `GET /api/instance/icon` | Icône de l'instance (publique : la page de connexion l'affiche) |
| `GET /api/instance` | Nom, setup requis, méthodes de connexion, domaine des apps (et, pendant le setup : fournisseurs DNS, ports 80/443 de la machine) |
| `POST /api/setup` | Termine le wizard avec un compte mot de passe (token de setup) |
| `POST /api/setup/discord` | Termine le wizard avec Discord : renvoie l'adresse d'autorisation |
| `POST /api/auth/login`, `POST /api/auth/logout` | Connexion par mot de passe, déconnexion |
| `GET /api/auth/link?token=` | Lien de secours |
| `GET /api/auth/discord`, `GET /api/auth/discord/callback` | OAuth Discord |
| `GET /api/caddy/ask?domain=` | Autorise un certificat à la demande pour Caddy |
| `GET /api/nodes/ca` | Certificat de la CA (pour vérifier l'empreinte) |
| `POST /api/nodes/join` | Join d'un agent (token de join) |

## Utilisateur connecté

| Route | Rôle |
|---|---|
| `GET /api/auth/me` | Compte courant, avec son profil |
| `PUT /api/me/profile` | Nom affiché (ou suivre Discord), description, email |
| `PUT`, `DELETE /api/me/avatar` | Envoyer une photo (data URL) ou revenir à Discord / l'initiale |
| `GET /api/users/{id}/avatar` | Photo envoyée par un utilisateur |
| `PUT /api/me/password` | Changer de mot de passe (comptes locaux) |
| `PUT`, `DELETE /api/me/banner` | Envoyer une bannière ou revenir à celle de Discord |
| `GET /api/users/{id}/banner` | Bannière envoyée par un utilisateur |
| `GET /api/members`, `GET /api/members/{id}` | Membres, et profil public (apps publiques, activité) |
| `PUT /api/apps/{id}/logo`, `GET /api/apps/{id}/logo` | Choisir le logo d'une app (auto, image, initiale) ; logo envoyé |
| `GET /api/logos?repo=` | Logo d'une image Docker Hub (mis en cache) |
| `PUT /api/apps/{id}/public` | Afficher ou masquer une app sur le profil de son propriétaire |
| `PUT /api/me/notifications`, `POST /api/me/notifications/test` | Webhook personnel, message de test |
| `GET`, `POST /api/me/ssh-keys`, `DELETE /api/me/ssh-keys/{id}` | Clés SSH de la passerelle |
| `GET /api/me/sessions`, `DELETE /api/me/sessions/{id\|others}` | Appareils connectés, en déconnecter un ou tous les autres |
| `GET /api/images/ports?image=` | Ports TCP et volumes qu'une image publique déclare, lus sur son registre |
| `GET`, `POST /api/support/tickets` | Demandes d'aide (les siennes, toutes pour un admin) ; en créer une |
| `GET /api/support/tickets/{id}` | Une demande et son fil |
| `POST /api/support/tickets/{id}/messages` | Répondre (rouvre une demande fermée) |
| `PUT /api/support/tickets/{id}/status` | Ouvrir ou fermer |
| `GET`, `POST /api/apps` | Lister, créer (`kind` : `web` ou `sandbox` ; `image` ou `dockerfile` ; `nodeId` facultatif, admins seulement) |
| `GET`, `PUT`, `DELETE /api/apps/{id}` | Voir, modifier, supprimer |
| `POST /api/apps/{id}/{start\|stop\|redeploy}` | Actions |
| `GET /api/apps/{id}/logs?tail=` | Logs en direct (SSE) |
| `GET /api/apps/{id}/usage` | CPU et mémoire de la dernière heure |
| `GET /api/apps/{id}/events` | Derniers événements |
| `GET /api/apps/{id}/network` | Chemin d'une requête vérifié étape par étape, réseaux et ports de l'app |
| `POST /api/apps/{id}/network/test` | Demander l'adresse de l'app depuis la machine de Forgeyard |
| `GET /api/apps/{id}/terminal` (WebSocket) | Shell dans le conteneur : binaire = frappes et sortie, texte = `resize` et `exit` |

| `GET`, `POST /api/stacks` | Lister, créer une stack (`name`, `compose`, `env`, `nodeId` facultatif, admins seulement) ; un compose refusé répond 400 avec `errors: [{line, message}]` |
| `POST /api/stacks/validate` | Vérifier un compose sans rien enregistrer : services dans l'ordre de démarrage, volumes, erreurs |
| `GET`, `PUT`, `DELETE /api/stacks/{id}` | Voir (avec compose et variables), modifier (seuls les services changés sont redéployés), supprimer (`?volumes=1` supprime aussi les volumes) |
| `POST /api/stacks/{id}/{start\|stop\|redeploy}` | Actions sur tous les services |
| `PUT`, `DELETE /api/stacks/{id}/backups/{volume}` | Planning de sauvegarde d'un volume (`schedule` `daily` ou `hourly`, `hour`, `keep`, `enabled`) |
| `POST /api/stacks/{id}/backups/{volume}/run` | Sauvegarder maintenant (202) |
| `GET /api/stacks/{id}/backups/{volume}/snapshots` | Sauvegardes du volume, les plus récentes d'abord |
| `POST /api/stacks/{id}/backups/{volume}/restore` | Restaurer une sauvegarde (`snapshot`) dans un volume neuf (202) |
| `POST /api/stacks/{id}/backups/{volume}/restores/{restore}/use` | Mettre le volume restauré à la place de celui de la stack (`confirm: true`) |

Les routes d'app et de stack vérifient que l'utilisateur est le propriétaire ou un admin. Un service de stack ne se modifie, ne se supprime ni ne change de node comme une app (409) : on passe par le compose.

## Admin

| Route | Rôle |
|---|---|
| `GET /api/admin/requests` | Demandes de compte |
| `POST /api/admin/requests/{id}/accept`, `/refuse` | Accepter (avec rôle) ou refuser |
| `GET`, `PUT /api/admin/settings/discord` | App Discord |
| `GET`, `POST /api/admin/nodes` | Lister, créer un node (`local: true` pour la machine de Forgeyard ; `lan`, `ingressMode`, `publicIp`, `ingressHttpPort` pour son réseau) |
| `POST /api/admin/nodes/{id}/join-command` | Nouvelle commande (node en attente) |
| `PUT /api/admin/nodes/{id}/ingress` | IP publique et mode d’entrée : `traefik` (directement), `proxy` (son reverse proxy), `relay` (par la machine de Forgeyard) |
| `DELETE /api/admin/nodes/{id}` | Retirer (refusé s'il a des apps) : son agent nettoie d'abord la machine ; répond `{cleaned, detail}` |
| `GET /api/admin/nodes/choices` | Nodes en ligne pour une nouvelle app, la recommandée en premier |
| `GET /api/admin/topology` | Nodes, réseaux Docker, conteneurs (IP, noms, ports, route) et problèmes, pour les vues Topologie et Tableau |
| `GET /api/admin/nodes/{id}/logs?tail=` | Logs de l'agent d'un node, en direct (SSE) |
| `POST /api/admin/nodes/{id}/check` | Vérifier un node : agent connecté, Traefik lancé, Traefik joignable par le chemin des visites |
| `POST /api/admin/nodes/{id}/update-agent` | Mettre à jour l'agent d'un node vers la version du server |
| `GET`, `PUT /api/admin/settings/agents` | Mise à jour automatique des agents, version du server |
| `POST /api/admin/apps/{id}/move` | Déplacer une app vers un autre node (`{nodeId}` ; `leaveData: true` pour une app avec des volumes, qui repart vide) |
| `GET`, `PUT /api/admin/settings/backups`, `POST …/test` | Stockage S3 des sauvegardes et mot de passe du dépôt restic ; ouvrir le dépôt depuis un node |
| `GET`, `PUT /api/admin/settings/notifications`, `POST …/test`, `POST …/test-support` | Webhooks du salon des admins et du salon support, types d'alertes, messages de test |
| `GET /api/admin/containers` | Conteneurs externes de tous les nodes en ligne |
| `GET /api/admin/nodes/{node}/containers/{id}/usage` | CPU et mémoire d'un conteneur externe, dernière heure |
| `GET /api/admin/nodes/{node}/containers/{id}/events` | Derniers événements d'un conteneur externe |
| `GET /api/admin/users` | Comptes, avec leur nombre d'apps |
| `PUT /api/admin/users/{id}` | Rôle, désactivé, apps suspendues (jamais le superadmin ni soi-même) |
| `DELETE /api/admin/users/{id}` | Supprimer un compte, ses apps et ses stacks (avec leurs volumes) |

## Superadmin

| Route | Rôle |
|---|---|
| `PUT /api/admin/settings/general` | Nom de l'instance, adresse de Forgeyard, vue de l'onglet Apps |
| `PUT`, `DELETE /api/admin/settings/icon` | Icône de l'instance (aussi son favicon) |
| `GET`, `PUT /api/admin/settings/domain` | Domaine, fournisseur DNS (et adresse de Forgeyard) |
| `POST /api/admin/nodes/{node}/containers/{id}/{start\|stop\|restart}` | Agir sur un conteneur externe |
| `GET /api/admin/nodes/{node}/containers/{id}/logs` | Logs d'un conteneur externe (SSE) |
| `GET /api/admin/nodes/{node}/containers/{id}/terminal` (WebSocket) | Shell dans un conteneur externe |
| `POST /api/admin/settings/domain/check` | Vérifier le domaine |
| `GET`, `PUT /api/admin/settings/login` | Connexion par mot de passe |
