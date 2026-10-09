# Architecture de Forgeyard

PaaS open source auto-hébergé : on installe le PaaS sur son serveur, puis on déploie des conteneurs Docker depuis une interface web, à partir d'un Dockerfile, d'un repo GitHub (déploiement automatique à chaque push, façon Vercel), et on peut ouvrir un shell SSH dans ses conteneurs.

> Statut : conception. Rien n'est encore implémenté.

---

## 1. Vue d'ensemble

Le PaaS est composé de deux programmes, issus d'un seul code Go :

- **`forgeyard server`** (le « chef ») : interface web, API, base de données, authentification, choix du node, webhooks GitHub, passerelle SSH. Il ne lance jamais de conteneur lui-même.
- **`forgeyard agent`** (l'« employé ») : installé sur chaque node. Il exécute les ordres du server sur sa machine (build, lancement et surveillance des conteneurs) et lui renvoie logs et métriques.

```
  Serveur A                                    Serveur B
┌─────────────────────────────┐            ┌──────────────────────┐
│  forgeyard server           │            │ forgeyard agent (B)  │
│   ▲                         │◄═══════════│                      │
│   │ localhost               │  tunnel    │                      │
│  forgeyard agent (node A)   │  gRPC+mTLS │                      │
│   │                         │  ouvert    │   │                  │
│   ▼                         │  par       │   ▼                  │
│  Docker A + Traefik         │  l'agent   │  Docker B + Traefik  │
└─────────────────────────────┘            └──────────────────────┘
```

Un **node** est une machine déclarée par l'admin pour faire tourner les conteneurs. Les utilisateurs ne choisissent jamais de node : le server place les apps automatiquement.

## 2. Stack technique

| Élément | Choix |
|---|---|
| Backend (server et agent) | Go |
| Frontend | React, intégré au binaire server via `go:embed` |
| Base de données | SQLite par défaut, requêtes via `sqlc` pour pouvoir passer à Postgres plus tard |
| Communication server ↔ agent | gRPC bidirectionnel, mTLS |
| Reverse proxy | Traefik sur chaque node, HTTPS automatique (Let's Encrypt) |
| Build sans Dockerfile | Railpack ou Nixpacks |
| Distribution | Image Docker et script d'installation en une commande |

### Organisation du code (monolithe modulaire)

```
forgeyard/
├── cmd/
│   ├── server/       control plane
│   └── agent/        agent de node
├── internal/
│   ├── auth/         Discord SSO, admin local, sessions
│   ├── api/          REST et websockets
│   ├── nodes/        enregistrement, capacité, santé
│   ├── deploy/       planification, cycle de vie, réconciliation
│   ├── build/        Dockerfile, GitHub, Railpack
│   ├── sshgateway/   passerelle SSH vers les conteneurs
│   └── notify/       webhook Discord, emails
├── proto/            définitions gRPC server ↔ agent
└── web/              front React
```

Pas de microservices : un seul binaire est bien plus simple à installer et à mettre à jour pour les utilisateurs du projet. La seule séparation nécessaire est server / agent, car l'agent tourne sur d'autres machines.

## 3. Installation et premier lancement

1. Installation : `curl ... | sh`, qui lance l'image Docker du server.
2. Le server affiche un **token de setup** dans ses logs. Il est demandé à l'ouverture du wizard, pour empêcher quelqu'un d'autre de s'approprier une instance fraîche.
3. **Wizard web** :
   1. saisie du token de setup ;
   2. **compte admin**, avec une seule méthode de connexion au choix (voir section 4) :
      - **Discord** : configuration de l'app Discord (Client ID, Secret, URL de redirection affichée), puis la première connexion Discord devient admin ;
      - **identifiant / mot de passe** : création du compte admin local. Discord pourra être configuré plus tard dans les réglages, pour les autres utilisateurs ;
   3. « Utiliser aussi ce serveur comme node ? », qui installe un agent local ;
   4. domaine de base et fournisseur DNS (Cloudflare : token API), facultatif ;
   5. création de la GitHub App via le *manifest flow* (un clic), facultative ;
   6. réglages généraux : nom du PaaS, domaine, webhook Discord de notification, SMTP (facultatif).

## 4. Authentification

Chaque compte a **une seule** méthode de connexion : Discord **ou** identifiant / mot de passe. On ne commence pas par un mot de passe pour ajouter Discord ensuite.

### Connexion Discord
- Chaque installation enregistre sa propre application OAuth sur le portail développeur Discord. Le wizard guide l'admin pas à pas.
- Les comptes sont identifiés par l'**ID Discord**, jamais par le pseudo.
- Scopes : `identify` et `email`.

### Compte local (identifiant / mot de passe)
- Hash argon2id, limitation des tentatives de connexion, 2FA TOTP facultative.
- Sur la page de login : bouton Discord en principal, petit lien « Connexion avec identifiant ».

### Secours
Pour ne jamais être bloqué hors de son PaaS (Discord en panne, app Discord mal configurée, mot de passe oublié), une commande à lancer sur le serveur génère un **lien de connexion admin à usage unique** :
`docker exec forgeyard forgeyard admin-login`

### Demandes de compte
1. Un compte Discord inconnu se connecte, ce qui crée une **demande en attente**.
2. L'admin est notifié par le **webhook Discord** et, si SMTP est configuré, par email.
3. L'admin accepte la demande en choisissant le rôle (`user` ou `admin`), ou la refuse.
4. L'utilisateur voit le statut de sa demande à chaque connexion (en attente, acceptée, refusée). Si SMTP est configuré, il reçoit un email **dans les deux cas**, acceptation comme refus (avec un motif facultatif).
5. Après coup, l'admin peut changer le rôle d'un compte ou le désactiver.

### Rôles
| Rôle | Droits |
|---|---|
| `superadmin` | Le **premier admin**, créé au wizard. Tous les droits d'un admin. Seul compte qui ne peut être ni modifié, ni rétrogradé, ni supprimé par un autre admin. Il peut transférer son rôle à un autre admin. |
| `admin` | Crée, modifie, désactive des comptes `user` et `admin`. Voit et gère les apps de **tous** les utilisateurs (vue par utilisateur), les nodes et les conteneurs externes. Valide les demandes (comptes, domaines, ports). |
| `user` | Gère uniquement ses propres apps, dans la limite de ses quotas. |

L'admin peut aussi créer un compte directement, sans attendre de demande : compte Discord (via l'ID Discord) ou compte avec identifiant et mot de passe.

## 5. Nodes

### Ajout d'un node
1. UI → Nodes → « Ajouter un node » : l'admin donne un nom et récupère une commande contenant un **token à usage unique** (valable 1 h).
2. Sur la machine : la commande installe Docker si besoin, l'agent (service systemd) et Traefik.
3. L'agent contacte le server avec le token et reçoit un **certificat client** propre au node. Le token est invalidé.
4. Le node apparaît en ligne dans l'UI avec ses caractéristiques (CPU, RAM, disque, OS).

**Retrait d'un node** : migration ou arrêt des apps, puis révocation du certificat.

### Communication
- C'est **l'agent qui ouvre la connexion** vers le server et la garde ouverte : le node n'a aucun port à ouvrir et peut être derrière un NAT.
- Le server stocke l'**état voulu** de chaque node (quelles apps, quelle version). L'agent **réconcilie** l'état réel avec cet état voulu, en continu et à chaque reconnexion.
- Si le tunnel coupe, les conteneurs continuent de tourner.

### Capacité
- Chaque app réserve du CPU et de la RAM (valeurs par défaut, modifiables dans la limite du quota de l'utilisateur).
- Capacité allouable d'un node = capacité totale moins une marge système (10 % par défaut).
- Un déploiement est accepté si `réservations existantes + nouvelle app ≤ capacité allouable`. Sinon le server essaie un autre node, puis refuse avec un message explicite.
- Sur-engagement configurable par l'admin (par exemple 150 % de la RAM).
- Métriques réelles (CPU, RAM, disque, réseau) remontées par l'agent pour les graphiques, conservées en mémoire avec un historique court.

### Santé
- Node injoignable ou saturé (disque, RAM) : statut « Indisponible », plus aucun nouveau déploiement.

### Conteneurs externes (admin)
L'agent détecte aussi les conteneurs Docker du node **qui n'ont pas été créés par le PaaS** (lancés à la main, par un autre outil…). On les distingue grâce aux labels que le PaaS pose sur ses propres conteneurs. Les conteneurs internes (agent, Traefik) sont exclus.

- Onglet « Conteneurs externes » par node, visible des admins uniquement : image, statut, ports, consommation CPU / RAM.
- Actions : logs, démarrer, arrêter, redémarrer, supprimer, shell.
- **Adopter** : transformer le conteneur en app du PaaS (rattachée à un utilisateur, avec domaine, suivi des crashs, etc.). Le PaaS recrée le conteneur à l'identique avec ses labels ; les volumes sont conservés.
- Leur consommation réelle est **comptée dans la capacité du node**, sinon le calcul de place disponible serait faux.

## 6. Apps et déploiements

Une **app** regroupe un conteneur, un domaine, des variables d'environnement, des volumes, des limites de ressources et des logs.

### Sources
1. **Image** existante (`nginx:latest`…) ou **Dockerfile** collé.
2. **Repo GitHub** via la GitHub App : repos privés, webhook à chaque push, statut sur les commits, choix de la branche.
3. **Sans Dockerfile** : détection automatique du langage (Railpack ou Nixpacks).

### Pipeline
push → clone → build (sur le node cible) → nouveau conteneur → healthcheck → bascule du trafic dans Traefik → arrêt de l'ancien conteneur. C'est un **déploiement sans coupure**.

- **Rollback** en un clic grâce aux dernières images conservées.
- Logs de build et d'exécution en direct (websocket).

### Domaines
Si l'admin a configuré un **domaine de base** (ex. `thomasforge.com`) et un **fournisseur DNS** (Cloudflare en premier, via un token API avec la permission *DNS:Edit*), le PaaS crée lui-même les enregistrements DNS.

**Apps web (HTTP)**
- Chaque app reçoit un sous-domaine selon un **modèle configurable** par l'admin, par défaut `{app}.thomasforge.com` → `monblog.thomasforge.com`. Le nom du node n'apparaît **pas** dans l'URL : l'adresse reste la même si l'app change de node.
- En cas de conflit de nom entre deux utilisateurs, le second doit choisir un autre nom (ou `{app}-{user}`).
- Le PaaS crée l'enregistrement DNS (vers l'IP du node) et Traefik sert l'app en HTTPS.
- **Pas de port dans l'URL** : tout passe par le 443 et Traefik aiguille selon le nom de domaine. L'utilisateur indique seulement le port interne de son app (ex. 3000).
- Le sous-domaine reste sur **un seul niveau** (`monblog.domaine.com`, pas `monblog.apps.domaine.com`) : il est couvert par le certificat gratuit de Cloudflare et par un seul certificat wildcard.
- Option proxy Cloudflare (nuage orange) pour masquer l'IP du node.

**Apps non-HTTP (TCP / UDP : serveur de jeu, base de données exposée…)**
- Elles ont besoin d'un **port public** sur le node, pris dans une plage définie par l'admin (ex. 20000-21000).
- Enregistrement DNS en mode « DNS only » : le proxy Cloudflare ne transporte que le HTTP.

**Validation par l'admin** — l'admin choisit le mode dans les réglages :
- **automatique** : le sous-domaine par défaut est créé dès le déploiement ;
- **sur demande** : l'utilisateur fait une demande, l'admin valide.

Dans tous les cas, les **sous-domaines personnalisés** et les **ports publics** passent par une demande validée par l'admin.

**Sans domaine configuré** : l'app reste accessible par l'IP du node et un port.

### Bases de données en un clic
Postgres, MySQL ou Redis, créées sur le même node que l'app, avec les variables de connexion injectées automatiquement dans l'app.

### Crashs
1. Redémarrage automatique avec délai croissant (5 s, 10 s, 30 s…).
2. Au-delà de 3 crashs en 5 minutes, l'app passe en **« Suspendue »** et n'est plus relancée.
3. On conserve les dernières lignes de logs, le code de sortie et l'indicateur OOM (tuée pour manque de mémoire).
4. L'admin est notifié. Bouton « Relancer » après diagnostic.

### Sécurité des conteneurs
- Interdits : `--privileged`, montages du disque de l'hôte, réseau de l'hôte.
- Un réseau Docker isolé par utilisateur.
- Quotas par utilisateur : CPU, RAM, nombre d'apps.

## 7. Accès SSH aux conteneurs

- L'utilisateur enregistre ses clés SSH publiques dans l'UI.
- **Passerelle SSH** intégrée au server : `ssh <app>@forgeyard.example.com -p 2222`.
- Le server vérifie la clé et les droits, puis relaie la session vers l'agent du bon node, qui ouvre un shell dans le conteneur (`docker exec`).
- Pas de `sshd` dans les conteneurs, pas de port ouvert par app.
- Bonus : terminal web dans le navigateur (xterm.js), même mécanisme.

## 8. Notifications

| Événement | Canal |
|---|---|
| Nouvelle demande de compte | Webhook Discord admin, email (facultatif) |
| Demande de compte acceptée ou refusée | Statut affiché à la connexion, email (si SMTP) |
| Demande de domaine ou de port | Webhook Discord admin |
| App suspendue après des crashs | Webhook Discord admin |
| Node indisponible | Webhook Discord admin |

Plus tard : messages privés Discord via un bot.

---

## 9. Plan de développement

### v0.1 : un node, déploiement d'image
- [x] Structure du projet Go (`cmd/server`, `cmd/agent`, `internal/`), front React intégré
- [x] SQLite, migrations, `sqlc`
- [x] Wizard : token de setup, admin identifiant / mot de passe, nom du PaaS
- [x] Connexion par identifiant, sessions
- [x] Commande `admin-login` de secours
- [x] Agent local : connexion gRPC, enregistrement par token, mTLS
- [x] Déployer une image Docker existante, avec ses variables d'environnement
- [x] Traefik : sous-domaine et HTTPS (80/443 ou derrière un proxy existant)
- [x] Domaine de base et DNS automatique (Cloudflare, OVH, Gandi, Porkbun)
- [x] Logs en direct, démarrer, arrêter, redéployer, supprimer

### v0.2 : comptes et multi-node
- [x] Discord SSO (y compris comme méthode de l'admin dans le wizard), demandes de compte, rôles
- [ ] Webhook Discord de notification
- [x] Ajout de nodes distants (commande join) — reste : script d'installation et agent en conteneur
- [ ] Capacité, placement automatique, métriques et graphiques
- [ ] Gestion des crashs et suspension
- [ ] Conteneurs externes : détection, gestion, adoption
- [ ] Demandes de domaines personnalisés et de ports publics, validation admin

### v0.3 : build et GitHub
- [ ] Build depuis un Dockerfile
- [ ] GitHub App (manifest flow), déploiement à chaque push
- [ ] Railpack ou Nixpacks
- [ ] Déploiement sans coupure, rollback

### v0.4 : confort
- [ ] Passerelle SSH et terminal web
- [ ] Bases de données en un clic
- [ ] Quotas utilisateurs
- [ ] SMTP et emails

### Plus tard
- Stacks docker-compose
- Preview deployments par pull request
- Réseau privé WireGuard entre nodes
- Migration d'apps entre nodes
- Option Postgres pour le server
- Bot Discord (messages privés)
- Scaling via Kubernetes
- Autres fournisseurs DNS que Cloudflare

## 10. Questions ouvertes
- Support de docker-compose : quand et sous quelle forme.
- Railpack ou Nixpacks.
- Nom du projet.
