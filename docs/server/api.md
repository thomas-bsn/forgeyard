# Routes de l'API

Toutes les écritures sont en JSON. Les routes sont déclarées dans `internal/api/router.go`.

## Publiques

| Route | Rôle |
|---|---|
| `GET /api/health` | Santé |
| `GET /api/instance` | Nom, setup requis, méthodes de connexion (et, pendant le setup : fournisseurs DNS, ports 80/443 de la machine) |
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
| `GET /api/auth/me` | Compte courant |
| `GET`, `POST /api/apps` | Lister, créer |
| `GET`, `PUT`, `DELETE /api/apps/{id}` | Voir, modifier, supprimer |
| `POST /api/apps/{id}/{start\|stop\|redeploy}` | Actions |
| `GET /api/apps/{id}/logs?tail=` | Logs en direct (SSE) |

Les routes d'app vérifient que l'utilisateur est le propriétaire ou un admin.

## Admin

| Route | Rôle |
|---|---|
| `GET /api/admin/requests` | Demandes de compte |
| `POST /api/admin/requests/{id}/accept`, `/refuse` | Accepter (avec rôle) ou refuser |
| `GET`, `PUT /api/admin/settings/discord` | App Discord |
| `GET`, `POST /api/admin/nodes` | Lister, créer un node (`local: true` pour la machine de Forgeyard) |
| `POST /api/admin/nodes/{id}/join-command` | Nouvelle commande (node en attente) |
| `PUT /api/admin/nodes/{id}/ingress` | IP publique et mode réseau |
| `DELETE /api/admin/nodes/{id}` | Retirer (refusé s'il a des apps) |

## Superadmin

| Route | Rôle |
|---|---|
| `GET`, `PUT /api/admin/settings/domain` | Adresse de Forgeyard, domaine, fournisseur DNS |
| `POST /api/admin/settings/domain/check` | Vérifier le domaine |
| `GET`, `PUT /api/admin/settings/login` | Connexion par mot de passe |
