# Projets docker-compose

Un **projet** = un `docker-compose.yml` collé dans Forgeyard (comme un Dockerfile collé) et ses variables, chiffrées. Chaque service devient une app du projet, avec ce qu'a toute app : déploiement, logs, terminal, événements, crashs. Toute le projet tourne sur **un seul node**.

Le même fichier tourne avec `docker compose up` sur un portable : Forgeyard n'ajoute que le bloc `x-forgeyard`, que Docker Compose ignore.

Code : `internal/compose` (lecture du fichier), `internal/api/stacks.go`, `internal/agent/stacks.go`, `web/src/Projects.tsx`. Dans le code, l'API et les noms Docker, un projet s'appelle encore une *stack* (`/api/stacks`, `forgeyard-stack-<id>`).

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
| `user` | Le compte du conteneur : un nom (`www-data`), un uid (`1000`), avec un groupe ou un gid si besoin (`1000:1000`, `app:app`). Les noms sont cherchés dans l'image. Sans, celui de l'image. Une autre forme est refusée. |
| `environment` | Objet ou liste `CLE=valeur` ; `CLE` seule prend la variable du projet du même nom. |
| `volumes` | Volumes **nommés** seulement (`nom:/chemin`, `nom:/chemin:ro`, ou la forme longue `type: volume`), déclarés sous `volumes:` à la racine. Dossiers de la machine (bind mounts), volumes anonymes et `tmpfs` refusés. |
| `depends_on` | Liste, ou objet avec `condition` : `service_started`, `service_healthy`, `service_completed_successfully`. |
| `healthcheck` | `test` (`["CMD", …]`, `["CMD-SHELL", "…"]`, une chaîne ou `["NONE"]`), `interval`, `timeout`, `start_period`, `retries`, `disable`. |
| `restart` | `no`, `always`, `on-failure[:N]`, `unless-stopped` ; sans, `unless-stopped`. |
| `volumes:` (racine) | Des noms, sans options (`driver`, `external`… refusés). |
| `name`, `version`, `x-…` | Ignorés (les ancres YAML `&` / `*` et `<<:` marchent). |
| `x-forgeyard` | `expose: { port: 80 }`, `backup: { type: postgres }`, `memory: 1024` (Mo, 512 par défaut). |

### Ce qui est refusé, et pourquoi

Une option n'est acceptée que si Forgeyard peut en reproduire le comportement. Aucune n'est ignorée en silence : elle est refusée, sur sa ligne, avec ce qu'il faut écrire à la place quand il y a une alternative.

| Option | Pourquoi | À la place |
| --- | --- | --- |
| `ports` | Forgeyard ne publie aucun port de la machine : `HOTE:CONTENEUR` n'a pas le même effet qu'une exposition HTTPS. | `x-forgeyard: { expose: { port: N } }`, avec le port du conteneur (le message le donne, d'après votre `ports`). Un seul service exposé par projet, en HTTP(S) ; pas de port TCP/UDP public. |
| `build` | Le projet ne reçoit que le compose, pas votre code. | Une image publiée. |
| `networks`, `network_mode` | Le projet a son propre réseau isolé. | Les services se joignent par leur nom. |
| `env_file` | Le fichier n'existe pas sur le node. | Les variables du projet et `${VAR}`. |
| `container_name` | Forgeyard nomme les conteneurs. | Le nom du service. |
| `privileged`, `cap_add`, `devices` | Ils sortent le conteneur de son isolation. | Aucune. |
| `extends` | Il lit d'autres fichiers. | Les ancres YAML (`&`, `*`, `<<:`). |

Les ports publics (allocation, validation, règles réseau) restent une décision à prendre à part : tant qu'elle ne l'est pas, `ports` reste refusé.

### Variables

`${VAR}`, `$VAR`, `${VAR:-défaut}` (aussi si vide), `${VAR-défaut}` (seulement si absente), `${VAR:?message}`, `${VAR:+autre}` ; `$$` donne un `$`. Elles viennent des **variables du projet**, chiffrées comme celles des apps. Une variable absente sans valeur par défaut est une erreur, pas une chaîne vide.

### Un seul service exposé

`x-forgeyard: { expose: { port: 80 } }` publie un service à `https://<projet>.<domaine>`, par Traefik, comme une app web (enregistrement DNS compris). Un seul service exposé par projet ; les autres n'ont aucun port publié.

## Noms

- Le service exposé est l'app **`<projet>`** (son sous-domaine), les autres **`<projet>-<service>`** (32 caractères au plus). Ces noms, et celui du projet, ne peuvent être pris ni par une app ni par un autre projet.
- Dans le projet, les services se joignent **par leur nom de service** : `postgres:5432`, `api:8080`.

## Réseau

Chaque projet a son réseau Docker, `forgeyard-stack-<id>`, où chaque service a l'alias de son nom. Un service privé n'est que sur ce réseau : ni les autres apps, ni Traefik ne le joignent. Le service exposé est aussi sur le réseau `forgeyard`, où Traefik l'atteint (et les autres apps par son nom d'app). L'onglet Réseau d'un service privé montre ce chemin.

## Démarrage

L'agent démarre les services dans l'ordre de leurs `depends_on`, sans jamais bloquer les autres apps du node :

- un service attend (état **« En attente de ses dépendances »**, avec ce qu'il attend) tant qu'une dépendance n'est pas démarrée, `healthy` (son healthcheck passe) ou terminée avec succès, selon la condition ; l'agent regarde de nouveau toutes les 2 s ;
- un service dont un autre attend la fin (`service_completed_successfully`) est un **job** : lancé une fois par version, jamais redémarré (`restart` forcé à `no`), affiché « Job terminé » ou « Job en échec (code N) » ;
- un job en échec **bloque** les services qui l'attendent (état « Bloquée ») : ses logs sont à un clic depuis la page du projet. Le corriger et redéployer relance le job, puis le reste ;
- une dépendance `service_healthy` sans healthcheck (ni dans le compose, ni dans l'image) bloque aussi, avec ce message.

Vérifié sur une copie de Shameless : migrate attend postgres healthy, api attend la fin de migrate, web attend api.

## Redéploiement

- **Modifier le compose** (ou les variables) : le server relit le fichier et compare service par service. Seuls les services dont l'image, la commande, le compte (`user`), les variables, les volumes, le healthcheck, le port ou la mémoire changent sont redéployés ; les autres ne sont pas touchés. Un service ajouté est créé, un service retiré supprimé (ses volumes restent : ce sont ceux du projet). Un service qui devient (ou cesse d'être) le service exposé change de nom : il est remplacé.
- Le service exposé garde le **déploiement sans coupure** des apps (la nouvelle version démarre à côté, puis prend le relais) ; un service avec des volumes est arrêté puis relancé, comme une app avec des volumes. Une nouvelle version qui attend ses dépendances laisse l'ancienne tourner.
- **Redéployer** recrée tous les services (images téléchargées de nouveau) et relance les jobs. **Arrêter** / **Démarrer** valent pour tous.
- Un service se redéploie aussi seul depuis sa page d'app. Sa configuration, elle, ne se change que par le compose : modifier ou supprimer le service comme une app, ou le déplacer de node, est refusé.

## Suppression

Supprimer le projet supprime ses apps (conteneurs, enregistrement DNS), son réseau et ses plannings de sauvegarde. Ses **volumes** ne sont supprimés que si on le coche (et qu'on tape le nom du projet) ; sinon ils restent sur le node. Les ids de projets ne sont jamais réutilisés : un nouveau projet ne retrouve pas les volumes ni les sauvegardes d'un ancien. Les sauvegardes déjà faites restent sur le stockage.

## Interface

Dans l'onglet **Apps** :

- **Nouvelle app › Docker Compose** : parcours en quatre étapes : importer et analyser, relire l'adaptation, configurer les ressources, confirmer. L'analyse récapitule chaque service une seule fois avec son image, son rôle, sa mémoire, ses dépendances et ses sauvegardes, plus les volumes du projet. Les erreurs de validation gardent leur ligne.

  L'adaptation locale retire les publications `ports` de l'hôte et permet de choisir un seul port TCP à publier en HTTPS avec `x-forgeyard.expose`. Les autres services restent privés et joignables par leur nom sur le réseau du projet. Les ports UDP sont retirés et signalés ; les autres options incompatibles ne sont pas transformées et doivent être corrigées manuellement. Le Compose adapté est relu avant d'être accepté ; seule la confirmation finale crée le projet. L'adaptation n'envoie pas le Compose à un fournisseur d'IA.

  Les variables chiffrées, le node, le registry privé et l'option de bind mounts se règlent après l'adaptation. Un admin choisit le node ; une demande de bind mounts reste soumise à approbation.

**Images privées.** Le bloc « Registry privé » du formulaire prend l'adresse du registry (`ghcr.io`, `docker.io`, `registry.exemple.fr`), un identifiant et un mot de passe ou un token (en lecture seule suffit). Le node s'en sert pour télécharger les images de ce registry, et d'elles seules : les autres images restent téléchargées sans identifiants. Le mot de passe est chiffré, comme les variables, et n'est jamais renvoyé ; à la modification, un champ vide le garde, et vider l'adresse retire le login. Un seul registry par projet.
- La liste montre d'abord les **Projets** (une carte par projet : état, adresse, une ligne par app), puis les **Apps** seules ; un filtre Tout / Apps / Projets n'en garde qu'un type. Les apps d'un projet ne sont listées que dans sa carte.
- Page d'un projet (`#/projects/<id>`) : état d'ensemble, adresse, node, réseau ; **Apps du projet** (état de chacune, ce qu'il attend, lien vers sa page d'app), **Logs** (un service à la fois), **Volumes et sauvegardes** (voir [sauvegardes](backups.md)), **Compose** ; boutons Arrêter / Redéployer ou Démarrer, Modifier le compose, Supprimer.


## Hors périmètre

`build:`, un projet sur plusieurs nodes, les bind mounts, les ports publics, les réseaux personnalisés, plusieurs services exposés.
