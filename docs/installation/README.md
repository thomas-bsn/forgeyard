# Installation

## Lancer

```bash
git clone https://github.com/thomas-bsn/forgeyard.git && cd forgeyard
docker compose up -d --build
docker logs forgeyard   # token de setup
```

`docker-compose.yml` lance deux conteneurs :

| Service | Rôle | Ports et volumes |
|---|---|---|
| `forgeyard` | le server | ports `8080` (web) et `8081` (agents) ; volumes `forgeyard-data:/data`, `forgeyard-join:/join` |
| `agent` | l'agent de cette machine | socket Docker, `/:/host:ro` (pour mesurer le disque), `forgeyard-agent:/state`, `forgeyard-join:/join` |

L'agent ne fait rien tant que la machine n'est pas activée comme node : il attend un fichier dans le volume partagé `/join` (voir [nodes/join](../nodes/join.md#la-machine-de-forgeyard-node-local)).

Pour ne faire tourner les apps que sur d'autres machines, désactivez l'agent local dans `docker-compose.override.yml` (`profiles: ["disabled"]`).

## Wizard de premier lancement

Au démarrage, tant que le setup n'est pas terminé, le server affiche un **token de setup** dans ses logs. Il n'est gardé qu'en mémoire : un redémarrage en affiche un nouveau. Il empêche quelqu'un d'autre de s'approprier une instance fraîche.

| Étape | Contenu |
|---|---|
| Token | Le token des logs (vérifié à la fin seulement) |
| Instance | Nom du PaaS, adresse de Forgeyard (par défaut celle du navigateur), « faire tourner les apps sur cette machine » |
| Domaine | Domaine des apps et token API du fournisseur DNS (ou « plus tard ») ; qui gère les ports 80/443 de cette machine |
| Méthode | Discord ou identifiant / mot de passe pour le superadmin |
| Compte admin | Identifiant et mot de passe (12 caractères min.), ou Client ID / Secret de l'app Discord |

Pour la question des ports 80/443, l'agent local a déjà regardé s'ils répondent (fichier `/join/host.json`) : s'ils sont pris, le wizard propose « Mon reverse proxy ». Voir [routing](../network/routing.md).

À la fin :

- **mot de passe** : tout est créé d'un coup (superadmin, réglages, domaine, node local), puis l'admin est connecté ;
- **Discord** : les réglages sont enregistrés, puis le navigateur part sur Discord. Le premier compte qui revient devient superadmin, et c'est à ce moment que le node local est créé. La connexion par mot de passe est alors désactivée (voir [auth](../auth/README.md)).

Les identifiants DNS sont vérifiés auprès du fournisseur avant d'être enregistrés : un mauvais token renvoie à l'étape Domaine.

## Mettre à jour

```bash
git pull
docker compose up -d --build
```

Les données restent dans le volume `forgeyard-data`.

## Modifier sans conflit

Ne modifiez ni `docker-compose.yml` ni le `Dockerfile` : copiez-les dans des fichiers ignorés par git.

- `docker-compose.override.yml` (à partir de `docker-compose.override.example.yml`) : ports, variables, reverse proxy, agent désactivé. Compose le fusionne automatiquement.
- `Dockerfile.local` : à brancher avec `build: { dockerfile: Dockerfile.local }` dans l'override.

## Réglages du server

| Option | Défaut | Rôle |
|---|---|---|
| `--addr` | `:8080` | Interface web et API |
| `--agent-addr` | `:8081` | Connexion des agents (gRPC, mTLS) |
| `--data-dir` | `data` (`/data` dans l'image) | Base, clés, certificats |
| `FORGEYARD_TRUSTED_PROXIES` | loopback et réseaux privés | Proxies autorisés à fixer `X-Forwarded-*` (IPs/CIDR séparés par des virgules, ou `none`) |
| `FORGEYARD_SECRET_KEY` | fichier `data/secret.key` | Clé de chiffrement des secrets (32 octets en base64) |
| `FORGEYARD_JOIN_DIR` | vide (`/join` dans Compose) | Dossier partagé avec l'agent local ; vide = pas de node local |
| `FORGEYARD_LOCAL_SERVER_URL` | `http://forgeyard:8080` | Adresse du server vue par l'agent local |
| `FORGEYARD_AGENT_IMAGE` | `ghcr.io/thomas-bsn/forgeyard-agent:latest` | Image affichée dans les commandes d'ajout de node |

Contenu du dossier de données : `forgeyard.db` (SQLite), `secret.key`, `ca.key` et `ca.crt` (autorité de certification des agents). Sauvegardez-le en entier : sans `secret.key`, les secrets enregistrés sont illisibles.

## Images publiées

Chaque push sur `main` lance les tests puis publie `ghcr.io/thomas-bsn/forgeyard` et `ghcr.io/thomas-bsn/forgeyard-agent` (amd64 et arm64). Le `Dockerfile` a deux cibles : `server` (par défaut) et `agent`.
