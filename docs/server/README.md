# Le server

## Organisation du code

Un monolithe modulaire en Go : un seul dépôt, deux binaires.

```
cmd/
  server/        point d'entrée du server (+ commande admin-login)
  agent/         point d'entrée de l'agent
internal/
  api/           routes HTTP, wizard, apps, nodes, domaine, Discord
  auth/          mots de passe, tokens, sessions
  discord/       client OAuth Discord
  dns/           fournisseurs DNS (libdns) ; dnstest pour les tests
  nodes/         hub gRPC : sessions des agents, états, métriques, logs
  pki/           CA privée et certificats
  secrets/       chiffrement des secrets
  store/         SQLite, migrations, requêtes sqlc
  agent/         agent : join, connexion, réconciliation, sondes
  docker/        client minimal de l'API Docker (socket Unix)
  agentpb/       code gRPC généré
proto/           protocole agent ↔ server
web/             interface React (Vite, TypeScript), intégrée au binaire avec go:embed
```

Pas de microservices : un binaire est plus simple à installer et à mettre à jour. La seule séparation est server / agent, parce que l'agent tourne sur d'autres machines.

## Base de données

- SQLite (`modernc.org/sqlite`, sans cgo), mode WAL, clés étrangères actives.
- Migrations SQL embarquées (`internal/store/migrations`), appliquées au démarrage dans l'ordre et notées dans `schema_migrations`.
- Requêtes écrites en SQL dans `internal/store/queries`, code Go généré par sqlc dans `internal/store/db`.

| Table | Contenu |
|---|---|
| `settings` | Réglages clé / valeur (nom, adresse, domaine, Discord…), secrets chiffrés |
| `users` | Comptes : Discord ou local, rôle, désactivé, apps suspendues, profil (nom et avatar Discord, description) |
| `sessions` | Hash des tokens de session, IP et navigateur d'origine |
| `avatars`, `banners` | Photos et bannières envoyées par les utilisateurs |
| `account_requests` | Demandes de compte Discord (en attente, refusées) |
| `login_links` | Liens de secours |
| `nodes` | Machines : hash du token de join, série du certificat, mode réseau, IP publique, local |
| `apps` | Apps : node, propriétaire, image, port, variables chiffrées, mémoire, état voulu, génération, nom DNS |
| `app_events` | Ce qui arrive à chaque app (200 derniers) |
| `container_events` | Ce qui arrive aux conteneurs externes, par node et nom (200 derniers) |

## Développement

| Commande | Effet |
|---|---|
| `make build` | Construit l'interface puis `bin/forgeyard-server` et `bin/forgeyard-agent` |
| `make run` | Construit et lance le server sur :8080 |
| `make dev-web` | Interface avec rechargement à chaud (Vite), à côté de `go run ./cmd/server` |
| `make test` | Tests Go |
| `make generate` | Régénère le code gRPC (buf) et les requêtes (sqlc) |

- [Routes de l'API](api.md)

## CI

`.github/workflows/ci.yml`, à chaque push et pull request :

```
go (vet + tests -race) ─┐
web (types + build)     ├─► images amd64/arm64 (publiées sur main et les tags vX.Y.Z)
code généré à jour      ┘
```

- **go** : `go vet ./...` et `go test -race ./...`.
- **web** : `npm ci` puis `npm run build`, qui vérifie les types (`tsc -b`) avant de construire.
- **generated** : relance `make generate` et échoue si `internal/agentpb` ou `internal/store/db` changent : après avoir modifié `proto/` ou les requêtes SQL, il faut commiter le code régénéré.
- Les images ne sont construites que si les trois passent ; une pull request les construit sans les publier.
