# Apps

Une **app** = un conteneur Docker lancé depuis une image, avec ses variables d'environnement, sa limite mémoire et son adresse.

Code : `internal/api/apps.go`, `web/src/Apps.tsx`.

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
| Image | ex. `nginx:alpine` (`:latest` ajouté si pas de tag) |
| Port | Le port **dans** le conteneur (ex. 3000) |
| Variables | 100 max., stockées chiffrées |
| Mémoire | 64 à 16384 Mo, 512 par défaut |

## Création

1. Validation, puis vérification que `nom.domaine` n'existe pas déjà dans le DNS (sinon 409 : ce nom sert déjà à un autre site).
2. **Placement** : le node en ligne qui a le moins d'apps. Aucun node en ligne : refus. L'app reste ensuite sur ce node.
3. Création de l'enregistrement DNS (si un fournisseur est configuré). En cas d'échec, l'app est annulée.
4. Envoi de l'état voulu au node, qui télécharge l'image et lance le conteneur (voir [agent](../nodes/agent.md#réconciliation)).

## Actions

| Action | Effet |
|---|---|
| Modifier | Image, port, variables, mémoire. Le conteneur est recréé. |
| Démarrer / Redéployer | Re-télécharge l'image et recrée le conteneur |
| Arrêter | Arrête le conteneur (il est gardé) |
| Configuration | Fenêtre : image, port, variables, mémoire ; enregistrer recrée le conteneur |
| Supprimer | Supprime l'enregistrement DNS créé par Forgeyard, puis le conteneur. Si le fournisseur DNS ne répond pas, l'app est quand même supprimée et l'erreur est notée dans les logs du server. |

L'onglet Apps a trois présentations, au choix du superadmin (Réglages › Général) :

- **Nodes à gauche** (par défaut) : la liste des nodes avec leur nombre d'apps et leurs problèmes, le CPU et la RAM du node choisi ; à droite ses apps en cartes (logo, nom, propriétaire, état, adresse) ;
- **Cartes de nodes** : une carte par node en haut (mini-logos de ses apps, problèmes), ses apps en tuiles en dessous ;
- **Icônes** : des onglets de nodes et les apps en grandes icônes avec une pastille d'état.

Partout « Tous » montre tous les nodes, le node choisi est retenu par le navigateur, les problèmes passent en premier, et on filtre par type (Forgeyard / externes), par état et par recherche (nom, image, propriétaire, projet compose). Un utilisateur ne choisit pas de node : il voit ses apps. Un conteneur externe arrêté avec un code de sortie non nul compte comme « en erreur ».

## États

`pending` (en attente du node), `pulling`, `creating`, `running`, `restarting`, `stopped`, `exited`, `error`, et `node-offline` si le node n'est pas connecté. L'UI rafraîchit toutes les 3 s et affiche aussi le code de sortie, « tuée par manque de mémoire » et le nombre de redémarrages.

## Adresse

- **Avec un domaine** : `https://<nom>.<domaine>`, routée par Traefik (voir [network](../network/README.md)).
- **Sans domaine** : le port de l'app est publié sur un port libre au hasard du node, et l'adresse est `http://<IP du node>:<port>`. L'IP est celle du node, sinon celle des réglages du domaine, sinon (node local) l'hôte de l'adresse de Forgeyard.

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

## Logs en direct

- `GET /api/apps/{id}/logs?tail=300` en Server-Sent Events (`/api/admin/nodes/{node}/containers/{id}/logs` pour un conteneur externe). Le server demande à l'agent d'ouvrir les logs Docker (`StartLogs`) et les relaie ; il ferme le flux (`StopLogs`) quand l'onglet se ferme.
- L'UI se reconnecte toute seule 2 s après une coupure (« Reconnexion… ») et garde les 2000 dernières lignes.
- Le bouton **En direct** met le flux en pause pour lire tranquillement. Le défilement automatique ne suit que si on est en bas.
