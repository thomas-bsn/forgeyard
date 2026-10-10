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
| server → agent | `DesiredState` (toutes les apps du node, mode réseau, et pour la machine de Forgeyard les relais vers les autres nodes de la même IP) | à la connexion, puis à chaque changement |
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
- conteneur absent et app voulue démarrée : pull (ou build depuis le Dockerfile de l'app), création, démarrage ;
- app arrêtée : conteneur arrêté (10 s pour s'arrêter proprement) ;
- conteneur `forgeyard.app` qui n'est plus dans l'état voulu : supprimé.

Réglages de chaque conteneur : réseau Docker `forgeyard`, redémarrage `unless-stopped`, limite mémoire (512 Mo par défaut) sans swap, pas de mode privilégié, `no-new-privileges`, logs limités à 3 × 10 Mo.

L'agent gère aussi le conteneur **Traefik** du node (voir [routing](../network/routing.md)) : créé quand le node a au moins une app, recréé si le mode réseau change, supprimé quand il n'y a plus d'app.

## Infos et métriques

Taille de la machine via Docker (`/info`) et gopsutil ; disque mesuré sur `FORGEYARD_HOST_ROOT` (le `/` de l'hôte monté en lecture seule dans `/host`), sinon sur `/`.

## Mise à jour

Les agents suivent la version du server : chaque binaire embarque le commit dont il est construit (`internal/version`, renseigné par `make build`, par la CI ou, dans `docker compose build`, lu dans `.git`), et l'agent l'annonce en se connectant.

- **Automatique** (Nodes › « Mise à jour auto des agents », activée par défaut) : un agent d'un autre commit que le server reçoit l'ordre de passer à l'image `ghcr.io/thomas-bsn/forgeyard-agent:sha-<commit du server>`, publiée par la CI pour chaque commit de `main`. Si elle n'est pas encore publiée (CI en cours), la page Nodes l'indique et le server réessaie toutes les 5 minutes (le temps que met la CI).
- **À la main** : bouton « Mettre à jour » sur la carte du node.
- **Comment** : l'agent télécharge la nouvelle image, puis lance un conteneur éphémère de cette image (`forgeyard-agent replace`) qui arrête son conteneur, le recrée à l'identique (volumes, variables, réseau, politique de redémarrage) avec la nouvelle image et le démarre. Si la nouvelle version ne tient pas 5 secondes, l'ancienne revient. Les apps ne sont pas touchées.
- **Exceptions** : l'agent de la machine de Forgeyard est géré par Docker Compose et se met à jour avec elle (`git pull && docker compose up -d --build`) ; un agent lancé hors conteneur se met à jour à la main. Un server construit sans commit (`go run`) ne demande rien.
- Un agent d'avant cette fonction se met à jour une dernière fois à la main (`docker pull`, `docker rm -f`, `docker run` avec la commande de la page Nodes, sans token).

## Commandes et options

| Commande | Effet |
|---|---|
| `forgeyard-agent join --server … --token … --ca …` | Rejoint puis tourne. Refuse si déjà rejoint. |
| `forgeyard-agent run` | Tourne. S'il n'a pas encore rejoint : rejoint avec les options si elles sont données, sinon attend le fichier de join. |
| `forgeyard-agent version` | Affiche le commit dont il est construit (`dev` sans). |
| `forgeyard-agent replace <conteneur> <image>` | Interne : recrée le conteneur de l'agent avec une autre image (voir Mise à jour). |

| Option | Variable | Rôle |
|---|---|---|
| `--state-dir` | `FORGEYARD_AGENT_STATE` | Dossier d'identité (`/state` dans l'image) |
| `--join-file` | `FORGEYARD_JOIN_FILE` | Fichier de join déposé par le server (node local) |
| `--agent-server` | `FORGEYARD_AGENT_SERVER` | `hôte:port` du port agent s'il diffère de l'adresse publique (ex. l'IP locale du server). Enregistré dans le dossier d'identité : il reste valable aux redémarrages et mises à jour, même sans l'option. |
| `--docker-host` | `DOCKER_HOST` | Socket Docker. Par défaut : `/var/run/docker.sock`, puis les sockets d'OrbStack, Docker Desktop et Colima. |
