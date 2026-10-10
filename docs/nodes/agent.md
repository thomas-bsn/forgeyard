# L'agent

## Connexion au server

Un seul flux gRPC bidirectionnel (`AgentService.Connect`, défini dans `proto/agent/v1/agent.proto`), toujours ouvert par l'agent.

| Sens | Message | Quand |
|---|---|---|
| agent → server | `Hello` (hostname, OS, arch, CPU, RAM, disque, version Docker) | premier message |
| agent → server | `Metrics` (CPU %, RAM et disque utilisés, conteneurs actifs) | toutes les 5 s |
| agent → server | `AppStatuses` (état, code de sortie, OOM, port publié, redémarrages, CPU et mémoire… par app) | toutes les 3 s |
| agent → server | `ExternalContainers` (conteneurs que Forgeyard n'a pas créés, avec CPU et mémoire) | toutes les 10 s |
| agent → server | `LogLine` | pendant qu'un utilisateur regarde des logs |
| server → agent | `Welcome` (id et nom du node) | après `Hello` |
| server → agent | `DesiredState` (toutes les apps du node + mode réseau) | à la connexion, puis à chaque changement |
| server → agent | `StartLogs` / `StopLogs` | ouverture / fermeture des logs dans l'UI (d'une app, ou d'un conteneur externe) |
| server → agent | `ContainerAction` | démarrer, arrêter ou redémarrer un conteneur externe |
| server → agent | `ExecStart` / `ExecInput` / `ExecResize` / `ExecClose` | terminal dans un conteneur |
| agent → server | `ExecOutput` | sortie du terminal, puis sa fin (code de sortie) |

- Coupure : l'agent se reconnecte avec un délai qui double de 1 s à 30 s. Les conteneurs continuent de tourner pendant ce temps.
- Si le server répond « node retiré » ou « certificat révoqué », l'agent s'arrête.
- Le server garde ~5 minutes de métriques en mémoire et note la dernière visite du node au plus une fois par minute.

## Réconciliation

Le server n'envoie pas d'ordres (« démarre X ») mais l'**état voulu** complet. L'agent compare avec ce qui tourne et corrige, à chaque `DesiredState` et toutes les 30 s. Ainsi, une coupure ou un conteneur supprimé à la main se répare tout seul.

Pour chaque app :

- conteneur `forgeyard-app-<id>`, labels `forgeyard.app=<id>` et `forgeyard.spec=<hash>` ;
- le hash couvre image, port, variables, adresse, mémoire, mode réseau et **génération** (incrémentée à chaque redéploiement ou modification) : s'il change, une nouvelle version est déployée sans coupure si l'app tourne (voir [apps](../apps/README.md#déploiement-sans-coupure)), sinon le conteneur est recréé ;
- conteneur absent et app voulue démarrée : pull, création, démarrage ;
- app arrêtée : conteneur arrêté (10 s pour s'arrêter proprement) ;
- conteneur `forgeyard.app` qui n'est plus dans l'état voulu : supprimé.

Réglages de chaque conteneur : réseau Docker `forgeyard`, redémarrage `unless-stopped`, limite mémoire (512 Mo par défaut) sans swap, pas de mode privilégié, `no-new-privileges`, logs limités à 3 × 10 Mo.

L'agent gère aussi le conteneur **Traefik** du node (voir [routing](../network/routing.md)) : créé quand le node a au moins une app, recréé si le mode réseau change, supprimé quand il n'y a plus d'app.

## Infos et métriques

Taille de la machine via Docker (`/info`) et gopsutil ; disque mesuré sur `FORGEYARD_HOST_ROOT` (le `/` de l'hôte monté en lecture seule dans `/host`), sinon sur `/`.

## Commandes et options

| Commande | Effet |
|---|---|
| `forgeyard-agent join --server … --token … --ca …` | Rejoint puis tourne. Refuse si déjà rejoint. |
| `forgeyard-agent run` | Tourne. S'il n'a pas encore rejoint : rejoint avec les options si elles sont données, sinon attend le fichier de join. |

| Option | Variable | Rôle |
|---|---|---|
| `--state-dir` | `FORGEYARD_AGENT_STATE` | Dossier d'identité (`/state` dans l'image) |
| `--join-file` | `FORGEYARD_JOIN_FILE` | Fichier de join déposé par le server (node local) |
| `--agent-server` | `FORGEYARD_AGENT_SERVER` | `hôte:port` du port agent s'il diffère de l'adresse publique (ex. l'IP locale du server). Lu à chaque démarrage, donc aussi pour un node déjà rejoint. |
| `--docker-host` | `DOCKER_HOST` | Socket Docker. Par défaut : `/var/run/docker.sock`, puis les sockets d'OrbStack, Docker Desktop et Colima. |
