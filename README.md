# Forgeyard

PaaS open source auto-hébergé : déployez des conteneurs Docker sur vos propres machines depuis une interface web, à partir d'une image, d'un Dockerfile ou d'un repo GitHub.

> En développement : pas encore prêt pour la production. Voir [ARCHITECTURE.md](ARCHITECTURE.md) pour la conception et le plan.

## Installation

```bash
git clone https://github.com/thomas-bsn/forgeyard.git && cd forgeyard
docker compose up -d
docker logs forgeyard   # affiche le token de setup
```

Ouvrez http://localhost:8080 (ou l'adresse de votre serveur), collez le token de setup et suivez le wizard.

Ports : `8080` pour l'interface web et l'API, `8081` pour les agents des nodes.

## Adapter à votre serveur

Ne modifiez jamais `docker-compose.yml` ni le `Dockerfile` : le prochain `git pull` entrerait en conflit avec vos changements. Utilisez plutôt ces fichiers, ignorés par git :

**Ports, réseaux Docker, variables d'environnement** → `docker-compose.override.yml`, que Docker Compose fusionne automatiquement.

```bash
cp docker-compose.override.example.yml docker-compose.override.yml
# décommentez ce dont vous avez besoin
```

**L'image elle-même** (par exemple pour ajouter un certificat d'autorité) → copiez le Dockerfile et faites construire Compose depuis la copie :

```bash
cp Dockerfile Dockerfile.local
```

```yaml
# docker-compose.override.yml
services:
  forgeyard:
    build:
      dockerfile: Dockerfile.local
```

## Mise à jour

```bash
git pull
docker compose up -d --build
```

Vos données sont conservées dans le volume `forgeyard-data`.

Si `git pull` signale des changements locaux, déplacez-les dans les fichiers d'override ci-dessus, puis lancez `git checkout -- docker-compose.yml Dockerfile`.

## Accès perdu

Discord en panne, application Discord mal configurée ou mot de passe oublié :

```bash
docker exec forgeyard forgeyard admin-login
```

La commande affiche un lien qui connecte en superadmin. Il est valable 15 minutes et ne sert qu'une fois.

## Développement

Prérequis : Go 1.27+, Node 24+.

```sh
make build              # front React puis binaires dans bin/
./bin/forgeyard-server  # http://localhost:8080, agents sur :8081
```

Avec rechargement à chaud du front : `go run ./cmd/server` dans un terminal, `make dev-web` dans un autre, puis http://localhost:5173.

`make generate` régénère le code gRPC (`proto/`) et les requêtes SQL (`internal/store/queries/`).
