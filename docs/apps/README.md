# Apps

Une **app** = un conteneur Docker lancé depuis une image (publiée, ou construite depuis un Dockerfile), avec ses variables d'environnement, sa limite mémoire et son adresse.

## D'où vient l'app

La fenêtre de création (et de configuration) a le formulaire à gauche et, à droite, l'aperçu de l'app (logo, adresse, node) qui suit la saisie, avec les boutons. On choisit la source en haut :

- **Image Docker** : une image publiée (Docker Hub, ghcr.io…), téléchargée par le node.
- **Dockerfile** : collé dans le formulaire (64 Ko max., avec une ligne `FROM`), le node construit l'image (état « Construction de l'image… ») avec `pull=1`, pour reprendre les mises à jour de l'image de base. Le Dockerfile est seul, sans autres fichiers : `COPY` ne trouve rien, le code se récupère avec `RUN git clone` ou `ADD` d'une URL. L'image est nommée `forgeyard/<nom>:<empreinte du Dockerfile>` : un nouveau Dockerfile donne une nouvelle image, déployée sans coupure, et l'agent supprime l'ancienne une fois remplacée (et celle d'une app supprimée). Une construction ratée affiche l'erreur et ses dernières lignes. Le logo automatique est celui de l'image du premier `FROM`.
- **Sandbox** : une machine Linux sans adresse web, à laquelle on se connecte en SSH (voir [Sandbox et SSH](#sandbox-et-ssh)).
- **GitHub** : bientôt.

Code : `internal/api/apps.go`, `web/src/Apps.tsx`.

## Sandbox et SSH

Une **sandbox** part d'une image Linux ordinaire (Debian 12 par défaut, Ubuntu 24.04, Alpine 3.21 en un clic, ou n'importe quelle image avec un `sh`) et tourne en continu : l'agent remplace sa commande par une boucle d'attente qui s'arrête aussitôt sur `docker stop`. Elle n'a ni port, ni sous-domaine, ni enregistrement DNS, ni route Traefik. Son `/root` est sur un volume du node (`forgeyard-sandbox-<id>`), gardé entre les redéploiements et supprimé avec l'app ; il ne suit pas un changement de node. Le type (app ou sandbox) se choisit à la création.

On s'y connecte par la **passerelle SSH** de Forgeyard, qui sert aussi pour les apps :

```bash
ssh monapp@<ip de Forgeyard> -p 2222            # un shell
ssh monapp@<ip de Forgeyard> -p 2222 uname -a   # une commande, avec son code de sortie
```

- La connexion se termine sur le server (port `2222`), qui l'envoie à l'agent du node par sa connexion habituelle, comme le terminal web : aucun serveur SSH dans le conteneur, un seul port à ouvrir sur la box (vers la machine de Forgeyard), et ça marche pour tous les nodes, même derrière un NAT.
- Le nom d'utilisateur est le **nom de l'app**. Sont acceptées les clés de son propriétaire (et des admins), ajoutées dans Mon profil › Clés SSH (ed25519, ECDSA, RSA de 2048 bits ou plus ; une clé n'appartient qu'à un compte). La clé d'hôte (`ssh_host_ed25519_key`, dans le dossier de données) est créée au premier démarrage.
- La page de l'app donne la commande, avec l'IP publique de l'instance (Réglages › Domaine) ou `FORGEYARD_SSH_HOST` : un domaine derrière le proxy Cloudflare ne laisse pas passer SSH.
- Pas encore : SFTP/`scp`, la redirection de ports. Une commande passe par un terminal (sa sortie arrive avec des `\r\n`). Ce qui est tapé avant que le shell soit prêt est gardé (64 Ko).

## Logo

Chaque app a un logo, au choix (clic sur le logo, sur la page de l'app) :

- **Automatique** (par défaut) : le logo de l'image sur Docker Hub (images officielles), sinon l'avatar de son éditeur (Gravatar de l'organisation ou de l'utilisateur Docker Hub). Le server les télécharge, les réduit en 128 px et les garde en cache (30 jours, 7 jours quand il n'y en a pas). Docker Hub n'a pas d'API documentée pour les logos : sans logo, ou pour une image d'un autre registre (ghcr.io…), c'est l'initiale.
- **Mon image** : envoyée et cadrée en carré.
- **Initiale et couleur**.

Les conteneurs externes ont le logo automatique de leur image.

## Champs

| Champ | Règle |
|---|---|
| Nom | `a-z`, `0-9`, `-`, 32 caractères max., unique. Devient le sous-domaine. Non modifiable. Réservés : `www`, `mail`, `forgeyard` et le sous-domaine de Forgeyard lui-même. |
| Image | ex. `nginx:alpine` (`:latest` ajouté si pas de tag) ; ou un Dockerfile |
| Port | Le port **dans** le conteneur (ex. 3000). Rempli tout seul avec celui que l'image déclare (`EXPOSE`), lu sur son registre sans la télécharger (`internal/registry`, réponses gardées 1 h), ou avec l'`EXPOSE` du Dockerfile. Tapé à la main, il n'est plus remplacé : le formulaire propose alors le port déclaré. Images privées ou registres du réseau local : pas de suggestion. Une fois l'app lancée, l'agent lit les ports TCP sur lesquels elle écoute vraiment (dans `/proc` de l'hôte, sans rien exécuter dans le conteneur, en ignorant ceux liés à `127.0.0.1`) : s'ils ne contiennent pas le port configuré, la page de l'app le signale (« Bad Gateway ») avec un bouton « Utiliser 3000 ». |
| Variables | 100 max., stockées chiffrées |
| Mémoire | 64 à 16384 Mo, 512 par défaut |

## Création

1. Validation, puis vérification que `nom.domaine` n'existe pas déjà dans le DNS (sinon 409 : ce nom sert déjà à un autre site).
2. **Placement** : un admin choisit la machine dans le formulaire ; sinon (et toujours pour un utilisateur), c'est la **machine recommandée** : celle qui a le plus de mémoire libre, puis le plus de CPU, puis le moins d'apps. Aucun node en ligne : refus.
3. Création de l'enregistrement DNS (si un fournisseur est configuré). En cas d'échec, l'app est annulée.
4. Envoi de l'état voulu au node, qui télécharge (ou construit) l'image et lance le conteneur (voir [agent](../nodes/agent.md#réconciliation)).

## Actions

| Action | Effet |
|---|---|
| Modifier | Image, port, variables, mémoire. Nouvelle version déployée sans coupure (voir ci-dessous). |
| Démarrer / Redéployer | Re-télécharge l'image et déploie une nouvelle version sans coupure |
| Arrêter | Arrête le conteneur (il est gardé) |
| Configuration | Même fenêtre que la création : source (image ou Dockerfile), port, variables, mémoire ; enregistrer déploie la nouvelle version sans coupure |
| Supprimer | Supprime l'enregistrement DNS créé par Forgeyard, puis le conteneur. Si le fournisseur DNS ne répond pas, l'app est quand même supprimée et l'erreur est notée dans les logs du server. |

Sur la page d'une app : une app arrêtée n'a que **Démarrer** (qui la déploie à neuf) ; en marche, **Arrêter** et **Redéployer**. Puis **Configuration**, et « Supprimer l'app… » en lien discret.

Dans la liste, le menu « ⋯ » d'une app (au survol, toujours visible sur mobile) reprend ces actions : ouvrir le site, démarrer ou arrêter, redéployer, changer de node (admins), supprimer.

L'onglet Apps a trois présentations, au choix du superadmin (Réglages › Général) :

- **Nodes à gauche** (par défaut) : la liste des nodes avec leur nombre d'apps et leurs problèmes, le CPU et la RAM du node choisi ; à droite ses apps en cartes (logo, nom, propriétaire, état, adresse) ;
- **Cartes de nodes** : une carte par node en haut (mini-logos de ses apps, problèmes), ses apps en tuiles en dessous ;
- **Icônes** : des onglets de nodes et les apps en grandes icônes avec une pastille d'état.

Par défaut, la liste montre les apps Forgeyard ; les conteneurs externes sont à un clic (filtre de type). Partout « Tous » montre tous les nodes, le node choisi est retenu par le navigateur, les problèmes passent en premier, et on filtre par type (Forgeyard / externes), par état et par recherche (nom, image, propriétaire, projet compose). Un utilisateur ne choisit pas de node : il voit ses apps. Un conteneur externe arrêté avec un code de sortie non nul compte comme « en erreur ».

## États

`pending` (en attente du node), `pulling`, `creating`, `running`, `restarting`, `stopped`, `exited`, `error`, et `node-offline` si le node n'est pas connecté. L'UI rafraîchit toutes les 3 s et affiche aussi le code de sortie, « tuée par manque de mémoire » et le nombre de redémarrages.

## Adresse

- **Avec un domaine** : `https://<nom>.<domaine>`, routée par Traefik (voir [network](../network/README.md)).
- **Sans domaine** : le port de l'app est publié sur un port libre au hasard du node, et l'adresse est `http://<IP du node>:<port>`. L'IP est celle du node, sinon celle des réglages du domaine, sinon (node local) l'hôte de l'adresse de Forgeyard.

## Changer de node

Un admin déplace une app depuis sa page (ligne « Tourne sur … · Changer de node », en haut) ou depuis la liste (menu « ⋯ » de l'app) :

1. l'app démarre sur le nouveau node pendant que l'ancien continue de la servir (« Déplacement depuis … ») ; l'ancien ne remonte plus son état, seul le nouveau le fait ;
2. dès qu'elle est en ligne sur le nouveau node, l'ancien arrête son conteneur ;
3. si les deux nodes n'ont pas la même IP publique, l'enregistrement DNS change tout de suite et l'ancien node la garde encore 6 minutes (le TTL du DNS est de 5 minutes).

Derrière la même box, le relais de la machine de Forgeyard suit : son propre routeur pour l'app reste prioritaire tant qu'elle y tourne, puis le relais vers le nouveau node prend le relais. Les données écrites dans le conteneur ne suivent pas (pas encore de volumes). Une app arrêtée change simplement de node.

## Déploiement sans coupure

Quand une app qui tourne change (redéploiement, nouvelle configuration), l'agent ne supprime pas l'ancien conteneur d'abord :

1. il télécharge l'image pendant que l'ancien conteneur sert toujours ;
2. il lance le nouveau à côté (état « Mise à jour sans coupure… ») et attend qu'il soit prêt : « healthy » si l'image a un `HEALTHCHECK`, sinon en marche depuis 5 secondes sans redémarrer (90 secondes au plus) ;
3. chaque version a son propre routeur Traefik, de priorité plus haute que la précédente : dès que Traefik voit la nouvelle, toutes les requêtes y vont ;
4. 3 secondes plus tard, l'ancien conteneur s'arrête (avec son délai de grâce pour les requêtes en cours) et le nouveau prend son nom.

Si la nouvelle version ne démarre pas (elle s'arrête, plante ou n'est pas prête à temps), elle est supprimée et **l'ancienne reste en ligne** ; l'app affiche l'erreur, et la même version n'est pas retentée tant qu'on ne redéploie pas. Mesuré en local : 0 erreur sur 2 286 requêtes pendant 3 redéploiements.

Une app arrêtée, ou sans conteneur, est simplement recréée.

## Crashs

Docker relance une app qui s'arrête toute seule (redémarrage `unless-stopped`). Forgeyard compte ces redémarrages, et les arrêts avec un code d'erreur ou par manque de mémoire :

- à chaque crash, le propriétaire est prévenu sur son webhook Discord (au plus une fois toutes les 10 minutes par app) ;
- **3 crashs en 5 minutes** : Forgeyard arrête l'app et la marque **« Suspendue (crashs) »**, avec un événement, et prévient le propriétaire et le salon des admins ;
- Démarrer ou Redéployer la relance et remet le compteur à zéro.

## Observabilité et événements

La page d'une app a trois vues :

- **Observabilité** : CPU (en % d'un cœur : 100 % = un cœur entier) et mémoire actuels, redémarrages, et les courbes de la dernière heure ; le survol d'une courbe affiche l'heure et la valeur, la légende la moyenne et le pic. L'agent mesure chaque conteneur via Docker (`/stats`) toutes les 3 s ; le server garde un point toutes les 15 s, en mémoire (perdu au redémarrage du server). Une mesure sans mémoire est ignorée : elle veut dire que Docker n'a pas pu lire le conteneur (agent trop ancien, ou noyau sans cgroup mémoire, fréquent sur Raspberry Pi). Une app au repos est vraiment à ~0 % de CPU.
- **Logs** : voir ci-dessous.
- **Événements** : création, DNS, configuration, démarrages et arrêts (avec qui l'a fait), mises en ligne, crashs (code de sortie, manque de mémoire), redémarrages en boucle. Les 200 derniers par app sont gardés en base.

## Conteneurs externes

L'agent liste aussi les conteneurs du node que Forgeyard n'a pas créés (lancés à la main, par docker compose…), toutes les 10 s. Ils apparaissent dans la colonne de leur node avec le badge **externe**, et appartiennent au superadmin.

- Même page qu'une app : **Observabilité** (CPU et mémoire, dernière heure), **Logs** et **Événements** (apparu, recréé, en ligne, arrêté, planté avec son code, redémarre en boucle, et les actions du superadmin), gardés par nom de conteneur pour survivre à un `docker compose up` qui le recrée.
- Les admins les voient ; seul le superadmin peut voir leurs logs, les démarrer, les arrêter ou les redémarrer.
- Les conteneurs de Forgeyard lui-même (server, agent, Traefik) sont exclus : leurs images portent le label `forgeyard.internal`, Traefik `forgeyard.ingress`, et l'agent reconnaît son propre conteneur.
- L'agent revérifie qu'un conteneur est bien externe avant toute action.

## Réseau

L'onglet **Réseau** de la page d'une app montre le **chemin d'une requête**, chaque étape vérifiée avec ce que Forgeyard sait (code : `internal/api/topology.go`, `web/src/Network.tsx`) :

| Étape | Vérifié |
|---|---|
| Visiteur | l'adresse de l'app |
| DNS | le nom est résolu : vers l'IP du node (ou de l'instance), via le proxy Cloudflare, ou ailleurs (avertissement) |
| Entrée | la box ou ton reverse proxy ; un node en mode Traefik derrière la même box que la machine de Forgeyard est signalé (c'est le 404 classique) |
| Relais | pour un node « via la machine de Forgeyard » : le relais existe, la machine de Forgeyard est en ligne, l'IP locale du node est connue |
| Traefik | le node est en ligne, son Traefik tourne, la route vise le port de l'app |
| Conteneur | l'app tourne et écoute vraiment sur ce port ; sinon « Bad Gateway », avec un bouton « Utiliser 3000 » |

Les étapes qu'on ne peut pas vérifier d'ici (redirection de la box) sont marquées comme telles. **Tester maintenant** demande l'adresse depuis la machine de Forgeyard, comme un visiteur (sans suivre les redirections) ; une box qui ne laisse pas une machine de chez soi se joindre par l'IP publique fait échouer ce test sans que l'app soit en cause.

En dessous : les réseaux Docker de l'app (sous-réseau, IP, noms), les apps du même propriétaire sur ces réseaux (les autres conteneurs sont seulement comptés) et ses ports (écoute réelle, route, ports publiés). Être sur le même réseau veut dire **pouvoir** se joindre, pas le faire. Chaque app est joignable sur son node par son nom : `http://grafana:3000`.

Pour une sandbox, le chemin est celui d'une connexion SSH : ton ordinateur → passerelle → agent du node → conteneur.

## Terminal

L'onglet **Terminal** de la page d'une app (et d'un conteneur externe, pour le superadmin) ouvre un shell dans le conteneur, dans le navigateur (xterm.js). L'agent cherche le shell dans les fichiers du conteneur, sans rien y exécuter : `bash`, sinon `sh`, `ash` ou `/busybox/sh`.

- Le navigateur parle au server en WebSocket (`/api/apps/{id}/terminal`) ; le server relaie à l'agent, qui ouvre un `docker exec` avec un TTY. Taille de la fenêtre suivie, couleurs, raccourcis.
- Seules les pages de l'instance peuvent l'ouvrir (Origin vérifiée), pour le propriétaire de l'app ou un admin. Chaque ouverture est notée dans les événements.
- L'app doit être en ligne. Une image minimale (distroless, scratch : `traefik/whoami` par exemple) n'a aucun shell : le terminal le dit au lieu d'ouvrir.

## Logs en direct

- `GET /api/apps/{id}/logs?tail=300` en Server-Sent Events (`/api/admin/nodes/{node}/containers/{id}/logs` pour un conteneur externe). Le server demande à l'agent d'ouvrir les logs Docker (`StartLogs`) et les relaie ; il ferme le flux (`StopLogs`) quand l'onglet se ferme.
- Chaque ligne affiche discrètement sa date et son heure (fournies par Docker).
- L'UI se reconnecte toute seule 2 s après une coupure (« Reconnexion… ») et garde les 2000 dernières lignes.
- Le bouton **En direct** met le flux en pause pour lire tranquillement. Le défilement automatique ne suit que si on est en bas.
