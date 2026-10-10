<p align="center"><img src="docs/assets/forgeyard-icon.png" alt="" width="96"></p>

# Forgeyard

PaaS open source auto-hébergé. Fonctionnement : [docs/](docs/README.md).

## Lancer

```bash
git clone https://github.com/thomas-bsn/forgeyard.git && cd forgeyard
docker compose up -d --build
docker logs forgeyard   # token de setup
```

Puis ouvrez http://<serveur>:8080.

## Mettre à jour

```bash
git pull
docker compose up -d --build
```

Les données sont conservées dans le volume `forgeyard-data`.

## Modifier sans conflit

Ne touchez ni à `docker-compose.yml` ni au `Dockerfile` : utilisez des copies ignorées par git.

```bash
cp docker-compose.override.example.yml docker-compose.override.yml   # ports, variables…
cp Dockerfile Dockerfile.local                                       # changements de l'image
```

Pour construire depuis `Dockerfile.local`, ajoutez dans `docker-compose.override.yml` :

```yaml
services:
  forgeyard:
    build:
      dockerfile: Dockerfile.local
```
