# Nodes

Un **node** est une machine qui fait tourner des apps. L'admin les ajoute ; les utilisateurs ne choisissent jamais de node.

Code : `internal/api/nodes.go`, `internal/api/localnode.go`, `internal/nodes` (côté server), `internal/agent` et `cmd/agent` (côté agent), `internal/pki`.

- [Rejoindre le PaaS](join.md) : token, certificats, node local.
- [L'agent](agent.md) : connexion au server, réconciliation, métriques, logs.

## États

| État | Signification |
|---|---|
| En attente (`pending`) | Créé dans l'UI, l'agent n'a pas encore rejoint |
| En ligne | L'agent est connecté en ce moment |
| Hors ligne | L'agent a rejoint mais n'est pas connecté. Ses conteneurs continuent de tourner. |

## Ajouter une autre machine

1. Nodes › Ajouter › « Une autre machine » : nom (`a-z`, `0-9`, `-`, 32 caractères max.).
2. L'UI affiche une commande à lancer sur la machine, valable 1 h :

   ```bash
   docker run -d --name forgeyard-agent --restart unless-stopped \
     -v /var/run/docker.sock:/var/run/docker.sock -v /:/host:ro -e FORGEYARD_HOST_ROOT=/host \
     -v forgeyard-agent:/state \
     ghcr.io/thomas-bsn/forgeyard-agent:latest run --server <adresse> --token <token> --ca sha256:<empreinte>
   ```

   ou, avec le binaire : `forgeyard-agent join --server … --token … --ca …`.
3. Le node passe « En ligne » avec son CPU, sa RAM, son disque et sa version de Docker.

Le port **8081** du server doit être joignable depuis la machine (connexion directe, jamais à travers un reverse proxy HTTP ni le proxy Cloudflare).

## Retirer un node

Refusé tant que le node héberge des apps. Sinon le node est supprimé et son agent déconnecté : à la reconnexion suivante, le server refuse son certificat et l'agent s'arrête. Pour rejoindre à nouveau, il faut effacer son dossier d'état (volume `forgeyard-agent`).

## Réseau d'un node

Chaque node a un mode de trafic web (Traefik sur 80/443, ou derrière votre proxy) et une IP publique facultative (sinon celle des réglages du domaine). Voir [routing](../network/routing.md).
