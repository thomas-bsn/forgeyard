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

Si l'agent est connecté mais ne joint pas Docker, la carte l'affiche en rouge avec l'erreur de Docker (socket non monté, permission…).

## Ajouter une autre machine

1. Nodes › « + Ajouter un node » › « Une autre machine » : nom (`a-z`, `0-9`, `-`, 32 caractères max.).
2. Une fenêtre affiche une commande à lancer sur la machine, valable 1 h :

   ```bash
   docker run -d --name forgeyard-agent --restart unless-stopped \
     -v /var/run/docker.sock:/var/run/docker.sock -v /:/host:ro -e FORGEYARD_HOST_ROOT=/host \
     -v forgeyard-agent:/state \
     ghcr.io/thomas-bsn/forgeyard-agent:latest run --server <adresse> --token <token> --ca sha256:<empreinte>
   ```

   ou, avec le binaire : `forgeyard-agent join --server … --token … --ca …`.
3. Le node passe « En ligne » avec son CPU, sa RAM, son disque et sa version de Docker.

Le port **8081** du server doit être joignable depuis la machine (connexion directe, jamais à travers un reverse proxy HTTP ni le proxy Cloudflare) :

- **machine sur le même réseau local** que Forgeyard : cochez « même réseau local » dans la fenêtre, la commande ajoute `--agent-server <IP locale>:8081` et l'agent ne passe pas par internet ;
- **machine ailleurs** : l'adresse de Forgeyard doit résoudre vers votre IP publique (avec Cloudflare : « DNS only », pas le nuage orange) et le port 8081 doit être redirigé sur la box.

Symptôme d'un port 8081 injoignable : le join réussit (il passe par HTTPS) mais le node reste hors ligne, et les logs de l'agent montrent `dial tcp …:8081: i/o timeout`. Pour corriger un node déjà rejoint, relancez son conteneur avec `--agent-server` : son identité est gardée dans le volume `forgeyard-agent`, pas besoin de nouveau token.

## Retirer un node

Refusé tant que le node héberge des apps. Sinon le node est supprimé et son agent déconnecté : à la reconnexion suivante, le server refuse son certificat et l'agent s'arrête. Pour rejoindre à nouveau, il faut effacer son dossier d'état (volume `forgeyard-agent`).

## Réseau d'un node

Réglé dans la fenêtre « Réseau… » de sa carte :

- **qui gère les ports 80/443** : Traefik de Forgeyard, ou votre reverse proxy (voir [routing](../network/routing.md)) ;
- **IP locale** : trouvée par l'agent, pré-remplie dans l'exemple de règle pour votre proxy ;
- **IP publique** : celle vers laquelle Forgeyard fait pointer le DNS des apps de ce node. Vide, c'est celle de Réglages › Domaine des apps : il ne faut la remplir que pour un node qui n'est pas derrière la même box (un VPS, un autre site).

## La carte d'un node

CPU, RAM et disque en direct ; nombre d'apps (dont en ligne) et de conteneurs en cours (dont hors Forgeyard) ; résumé du réseau. Les apps et les conteneurs externes de chaque node sont listés dans l'onglet Apps, une colonne par node (voir [apps](../apps/README.md#conteneurs-externes)).
