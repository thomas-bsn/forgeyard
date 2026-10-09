# Forgeyard

PaaS open source auto-hébergé : déployez des conteneurs Docker sur vos propres machines depuis une interface web, à partir d'une image, d'un Dockerfile ou d'un repo GitHub.

> En développement, rien n'est encore utilisable. Voir [ARCHITECTURE.md](ARCHITECTURE.md) pour la conception et le plan.

## Développement

Prérequis : Go 1.27+, Node 24+.

```sh
make build            # build du front React puis des binaires dans bin/
./bin/forgeyard-server  # http://localhost:8080
```

Avec rechargement à chaud du front : `go run ./cmd/server` dans un terminal, `make dev-web` dans un autre, puis http://localhost:5173.

## Accès perdu

Discord en panne, app Discord mal configurée ou mot de passe oublié : sur le serveur, lancez

```sh
./bin/forgeyard-server admin-login
```

La commande affiche un lien qui connecte en superadmin. Il est valable 15 minutes et ne sert qu'une fois.
