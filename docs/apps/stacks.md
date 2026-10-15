# Stacks docker-compose

Une **stack** = un `docker-compose.yml` collé dans Forgeyard (comme un Dockerfile collé) et ses variables, chiffrées. Chaque service devient une app de la stack, avec ce qu'a toute app : déploiement, logs, terminal, événements, crashs. Toute la stack tourne sur **un seul node**.

Le même fichier tourne avec `docker compose up` sur un portable : Forgeyard n'ajoute que le bloc `x-forgeyard`, que Docker Compose ignore.

Code : `internal/compose` (lecture du fichier), `internal/api/stacks.go`, `internal/agent/stacks.go`, `web/src/Stacks.tsx`.

## Exemple

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: shameless
      POSTGRES_DB: shameless
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U shameless"]
      interval: 5s
    x-forgeyard:
      backup: { type: postgres }   # sauvegardé avec pg_dump

  migrate:                         # un job : api attend qu'il se termine
    image: ghcr.io/shameless/api:${TAG:-latest}
    command: ["dotnet", "Shameless.Api.dll", "--migrate"]
    environment:
      ConnectionStrings__Default: Host=postgres;Database=shameless;Username=shameless;Password=${DB_PASSWORD}
    depends_on:
      postgres: { condition: service_healthy }

  api:
    image: ghcr.io/shameless/api:${TAG:-latest}
    depends_on:
      postgres: { condition: service_healthy }
      migrate: { condition: service_completed_successfully }

  web:                             # Caddy, qui sert /api en le passant à api:8080
    image: ghcr.io/shameless/web:${TAG:-latest}
    depends_on: [api]
    x-forgeyard:
      expose: { port: 80 }         # https://shameless.<domaine>

volumes:
  pgdata:
```

## Ce qui est pris en charge

Tout le reste est refusé, avec la ligne et la raison (tous les problèmes d'un coup) :

| Clé | Règle |
|---|---|
| `services.<nom>` | `a-z`, `0-9`, `-`, 32 caractères max. ; 20 services max. |
| `image` | Obligatoire : une image publiée. `build` est refusé. |
| `command`, `entrypoint` | Liste, ou chaîne découpée comme le ferait un shell (sans en lancer un). |
| `environment` | Objet ou liste `CLE=valeur` ; `CLE` seule prend la variable de la stack du même nom. |
| `volumes` | Volumes **nommés** seulement (`nom:/chemin`, `nom:/chemin:ro`, ou la forme longue `type: volume`), déclarés sous `volumes:` à la racine. Dossiers de la machine (bind mounts), volumes anonymes et `tmpfs` refusés. |
| `depends_on` | Liste, ou objet avec `condition` : `service_started`, `service_healthy`, `service_completed_successfully`. |
| `healthcheck` | `test` (`["CMD", …]`, `["CMD-SHELL", "…"]`, une chaîne ou `["NONE"]`), `interval`, `timeout`, `start_period`, `retries`, `disable`. |
| `restart` | `no`, `always`, `on-failure[:N]`, `unless-stopped` ; sans, `unless-stopped`. |
| `volumes:` (racine) | Des noms, sans options (`driver`, `external`… refusés). |
| `name`, `version`, `x-…` | Ignorés (les ancres YAML `&` / `*` et `<<:` marchent). |
| `x-forgeyard` | `expose: { port: 80 }`, `backup: { type: postgres }`, `memory: 1024` (Mo, 512 par défaut). |

Refusés avec une explication : `ports` (exposer avec `x-forgeyard`), `networks`, `env_file` (mettre les variables dans la stack), `container_name`, `privileged`, `cap_add`, `devices`, `network_mode`, `extends`.

### Variables

`${VAR}`, `$VAR`, `${VAR:-défaut}` (aussi si vide), `${VAR-défaut}` (seulement si absente), `${VAR:?message}`, `${VAR:+autre}` ; `$$` donne un `$`. Elles viennent des **variables de la stack**, chiffrées comme celles des apps. Une variable absente sans valeur par défaut est une erreur, pas une chaîne vide.

### Un seul service exposé

`x-forgeyard: { expose: { port: 80 } }` publie un service à `https://<stack>.<domaine>`, par Traefik, comme une app web (enregistrement DNS compris). Un seul service exposé par stack ; les autres n'ont aucun port publié.

## Noms

- Le service exposé est l'app **`<stack>`** (son sous-domaine), les autres **`<stack>-<service>`** (32 caractères au plus). Ces noms, et celui de la stack, ne peuvent être pris ni par une app ni par une autre stack.
- Dans la stack, les services se joignent **par leur nom de service** : `postgres:5432`, `api:8080`.

## Réseau

Chaque stack a son réseau Docker, `forgeyard-stack-<id>`, où chaque service a l'alias de son nom. Un service privé n'est que sur ce réseau : ni les autres apps, ni Traefik ne le joignent. Le service exposé est aussi sur le réseau `forgeyard`, où Traefik l'atteint (et les autres apps par son nom d'app). L'onglet Réseau d'un service privé montre ce chemin.

## Démarrage

L'agent démarre les services dans l'ordre de leurs `depends_on`, sans jamais bloquer les autres apps du node :

- un service attend (état **« En attente de ses dépendances »**, avec ce qu'il attend) tant qu'une dépendance n'est pas démarrée, `healthy` (son healthcheck passe) ou terminée avec succès, selon la condition ; l'agent regarde de nouveau toutes les 2 s ;
- un service dont un autre attend la fin (`service_completed_successfully`) est un **job** : lancé une fois par version, jamais redémarré (`restart` forcé à `no`), affiché « Job terminé » ou « Job en échec (code N) » ;
- un job en échec **bloque** les services qui l'attendent (état « Bloquée ») : ses logs sont à un clic depuis la page de la stack. Le corriger et redéployer relance le job, puis le reste ;
- une dépendance `service_healthy` sans healthcheck (ni dans le compose, ni dans l'image) bloque aussi, avec ce message.

Vérifié sur une copie de Shameless : migrate attend postgres healthy, api attend la fin de migrate, web attend api.

## Redéploiement

- **Modifier le compose** (ou les variables) : le server relit le fichier et compare service par service. Seuls les services dont l'image, la commande, les variables, les volumes, le healthcheck, le port ou la mémoire changent sont redéployés ; les autres ne sont pas touchés. Un service ajouté est créé, un service retiré supprimé (ses volumes restent : ce sont ceux de la stack). Un service qui devient (ou cesse d'être) le service exposé change de nom : il est remplacé.
- Le service exposé garde le **déploiement sans coupure** des apps (la nouvelle version démarre à côté, puis prend le relais) ; un service avec des volumes est arrêté puis relancé, comme une app avec des volumes. Une nouvelle version qui attend ses dépendances laisse l'ancienne tourner.
- **Redéployer** recrée tous les services (images téléchargées de nouveau) et relance les jobs. **Arrêter** / **Démarrer** valent pour tous.
- Un service se redéploie aussi seul depuis sa page d'app. Sa configuration, elle, ne se change que par le compose : modifier ou supprimer le service comme une app, ou le déplacer de node, est refusé.

## Suppression

Supprimer la stack supprime ses apps (conteneurs, enregistrement DNS), son réseau et ses plannings de sauvegarde. Ses **volumes** ne sont supprimés que si on le coche (et qu'on tape le nom de la stack) ; sinon ils restent sur le node. Les ids de stacks ne sont jamais réutilisés : une nouvelle stack ne retrouve pas les volumes ni les sauvegardes d'une ancienne. Les sauvegardes déjà faites restent sur le stockage.

## Interface

Onglet **Stacks** :

- **Nouvelle stack** : nom, `docker-compose.yml`, variables. Le fichier est vérifié pendant la frappe : erreurs avec leur ligne (un clic sélectionne la ligne), variables utilisées mais absentes proposées en un clic, et à droite l'aperçu des services dans leur ordre de démarrage (exposé, privé, job, `pg_dump`). Un admin choisit le node.
- Page d'une stack : état d'ensemble, adresse, node, réseau ; **Services** (état de chacun, ce qu'il attend, lien vers sa page d'app), **Logs** (un service à la fois), **Volumes et sauvegardes** (voir [sauvegardes](backups.md)), **Compose** ; boutons Arrêter / Redéployer ou Démarrer, Modifier le compose, Supprimer.

Les services apparaissent aussi dans l'onglet Apps, avec « service … · stack » ou « job … · stack » à la place d'une adresse.

## Hors périmètre

`build:`, une stack sur plusieurs nodes, les bind mounts, les ports publics, les réseaux personnalisés, plusieurs services exposés.
