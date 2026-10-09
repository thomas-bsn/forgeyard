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
