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

## Topologie et tableau

La page Nodes a trois vues (choix retenu par le navigateur) :

- **Cartes** : une carte par machine (ci-dessous).
- **Topologie** : trois colonnes, Internet (les visiteurs), Entrée (la box : IP publique, ports 80/443 et 2222 vers la machine de Forgeyard, domaine), puis chaque node avec son Traefik et ses réseaux Docker en cadres pointillés (le réseau `forgeyard` en violet, ceux des autres conteneurs en orange), les conteneurs dedans (`it-tools :80`, ou `route :80 · écoute :3000` avec un « ! » rouge en cas de problème). Les flèches d'entrée (visiteurs → box → Traefik) et de relais (« relais LAN », autour des nodes) sont toujours là ; les routes vers les apps se montrent avec « Routes ». Cliquer un conteneur allume son chemin et estompe le reste ; « Liens possibles » relie alors le conteneur à ceux qui partagent un de ses réseaux (ils peuvent se joindre, sans dire qu'ils le font) ; « Réseaux » enlève les cadres ; « Conteneurs externes » les ajoute (masqués par défaut). Le panneau de droite détaille le conteneur (IP, alias, réseaux, ports, route, problème et sa correction, « Ouvrir l'app », « Voir le chemin complet ») ou liste les problèmes.
- **Tableau** : une ligne par conteneur, groupée par node : réseaux et IP, ports en écoute, ports publiés, route Traefik, problème. Filtre (nom, image, IP, réseau), problèmes d'abord, conteneurs externes au choix (masqués par défaut, comme dans la Topologie). C'est la vue qui tient avec beaucoup de conteneurs.

Les données viennent de l'agent, qui décrit toutes les 10 secondes les réseaux Docker de son node et, pour chaque conteneur, ses adresses et noms sur chaque réseau, ses ports publiés et ceux sur lesquels il écoute (lus dans `/proc` de l'hôte). Les réseaux par défaut de Docker sans conteneur ne sont pas affichés. Un agent d'avant cette fonction n'envoie rien : la vue le signale.

## Réseau d'un node

Réglé dans la fenêtre « Réseau… » de sa carte :

- **qui gère les ports 80/443** : Traefik de Forgeyard, ou votre reverse proxy (voir [routing](../network/routing.md)) ;
- **IP locale** : trouvée par l'agent, pré-remplie dans l'exemple de règle pour votre proxy ;
- **IP publique** : celle vers laquelle Forgeyard fait pointer le DNS des apps de ce node. Un node derrière la même box que Forgeyard (même IP) doit être en mode « Mon reverse proxy » : la machine de Forgeyard lui relaie ses apps (voir [routing](../network/routing.md#plusieurs-nodes-derrière-la-même-box)). Vide, c'est celle de Réglages › Domaine des apps : il ne faut la remplir que pour un node qui n'est pas derrière la même box (un VPS, un autre site).

## La carte d'un node

CPU, RAM et disque en direct (le disque est celui où Docker range ses images et conteneurs, `DockerRootDir`, par exemple le volume de données d'un NAS plutôt que sa petite partition système ; à défaut, la racine de la machine) ; nombre d'apps (dont en ligne) ; conteneurs en cours (apps et conteneurs hors Forgeyard ; ceux de Forgeyard lui-même, l'agent et Traefik, ne comptent pas) ; résumé du réseau. Les logiciels installés hors Docker (Pi-hole installé directement, par exemple) n'apparaissent pas. Les apps et les conteneurs externes de chaque node sont listés dans l'onglet Apps, en choisissant le node (voir [apps](../apps/README.md#conteneurs-externes)).
