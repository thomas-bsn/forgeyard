# Feuille de route

## v0.1 : un node, déploiement d'image
- [x] Server et agent en Go, interface React intégrée
- [x] SQLite, migrations, sqlc
- [x] Wizard : token de setup, compte admin, nom, domaine, reverse proxy, machine de Forgeyard comme node
- [x] Connexion par identifiant, sessions, lien de secours `admin-login`
- [x] Agent : join par token, mTLS, réconciliation
- [x] Déployer une image Docker avec ses variables d'environnement
- [x] Traefik : sous-domaine et HTTPS (80/443 ou derrière un proxy existant, détecté au setup)
- [x] DNS automatique (Cloudflare, OVH, Gandi, Porkbun)
- [x] Logs en direct, démarrer, arrêter, redéployer, supprimer

## v0.2 : comptes et multi-node
- [x] Discord SSO, demandes de compte, rôles
- [x] Profils : photo et bannière (Discord ou envoyées, recadrées), description, appareils connectés, mot de passe
- [x] Membres : liste, profils publics (apps publiques, activité, contact Discord)
- [x] Interface : menu du compte, trois vues de l'onglet Apps, logos d'apps (Docker Hub, image, initiale), icône de l'instance, page de connexion
- [x] Ajout de machines (commande `docker run` ou binaire)
- [x] Gestion des comptes : liste, changement de rôle, suspension des apps, désactivation, suppression
- [ ] Création directe de comptes, transfert du superadmin
- [ ] Clés SSH et jetons d'API (onglets « bientôt » du profil)
- [ ] Bot Discord : messages privés, photos et bannières à jour sans reconnexion
- [x] Notifications Discord (webhook) : salon des admins et webhook personnel
- [x] Métriques et graphiques par app, journal d'événements
- [x] Placement sur la machine qui a le plus de mémoire libre, choix du node par les admins, déplacement d'une app entre nodes sans coupure
- [ ] Capacité : réservations de CPU et de RAM par app
- [x] Gestion des crashs et suspension (3 crashs en 5 minutes)
- [x] Conteneurs externes : détection, logs, démarrer / arrêter / redémarrer
- [ ] Conteneurs externes : adoption (en faire une vraie app Forgeyard)
- [ ] Demandes de domaines personnalisés et de ports publics, validation admin

## v0.3 : build et GitHub
- [x] Build depuis un Dockerfile collé (sans autres fichiers)
- [ ] GitHub App (manifest flow), déploiement à chaque push
- [ ] Railpack ou Nixpacks
- [x] Déploiement sans coupure (l'ancienne version reste en ligne si la nouvelle ne démarre pas)
- [ ] Rollback vers une version précédente

## v0.4 : confort
- [x] Terminal web dans les conteneurs
- [ ] Passerelle SSH (clés dans le profil)
- [ ] Bases de données en un clic
- [ ] Quotas utilisateurs
- [ ] SMTP et emails

## Plus tard
- Stacks docker-compose
- Preview deployments par pull request
- Réseau privé WireGuard entre nodes
- Volumes persistants (les données suivraient aussi un changement de node)
- Option Postgres pour le server
- Bot Discord (messages privés)
- Utiliser directement le Traefik de l'utilisateur s'il en a déjà un
- Scaling via Kubernetes

## Limites actuelles
- Une app ne peut pas être renommée.
- Le déploiement sans coupure ne vaut que pour les apps web (avec un domaine) : une app publiée sur un port du node change de port à chaque version.
- Les photos Discord ne se mettent à jour qu'à la connexion Discord (pas de bot).
- Les logos Docker Hub viennent d'une API non documentée : sans logo, l'initiale.
- L'historique CPU / mémoire n'est gardé qu'en mémoire : il repart de zéro quand le server redémarre.
- Changer de domaine ne supprime pas les anciens enregistrements DNS.
- Pas d'en-têtes de sécurité HTTP ni d'email ACME (voir [sécurité](security/README.md#limites-connues)).

---

## Conception des fonctionnalités à venir

### Capacité
- Chaque app réserve du CPU et de la RAM. Capacité allouable d'un node = capacité totale moins une marge système (10 % par défaut).
- Un déploiement est accepté si `réservations existantes + nouvelle app ≤ capacité allouable`. Sinon le server essaie un autre node, puis refuse avec un message clair.
- Sur-engagement réglable par l'admin (ex. 150 % de la RAM).
- Node injoignable ou saturé : « Indisponible », plus aucun nouveau déploiement.

### Crashs
Fait : voir [apps](apps/README.md#crashs). Reste : garder les dernières lignes de logs du crash avec l'événement.

### Conteneurs externes (admin)
- Fait : détection, logs, observabilité, événements, démarrer / arrêter / redémarrer.
- Reste : supprimer, shell, et **adopter** : recréer le conteneur à l'identique comme app Forgeyard (volumes conservés).
- Leur consommation compte dans la capacité du node.

### Domaines et ports
- Modèle de sous-domaine réglable (par défaut `{app}.domaine`), validation automatique ou par l'admin.
- Sous-domaines personnalisés et ports publics (apps TCP/UDP, plage définie par l'admin) : sur demande validée par un admin. Les ports publics sont en « DNS only ».

### Sources de déploiement
- Dockerfile collé ou repo GitHub via une GitHub App : repos privés, webhook à chaque push, statut sur les commits, choix de la branche.
- Sans Dockerfile : détection du langage (Railpack ou Nixpacks).
- Pipeline : push → clone → build sur le node → nouveau conteneur → healthcheck → bascule dans Traefik → arrêt de l'ancien. Rollback grâce aux dernières images.

### SSH
- Clés publiques enregistrées dans l'UI, passerelle intégrée au server : `ssh <app>@forgeyard.example.com -p 2222`.
- Le server vérifie la clé et les droits, puis relaie vers l'agent, qui ouvre un shell dans le conteneur (`docker exec`). Pas de `sshd` dans les conteneurs.
- Terminal web (xterm.js) avec le même mécanisme.

### Notifications

| Événement | Canal |
|---|---|
| Nouvelle demande de compte | Webhook Discord admin, email (facultatif) |
| Demande acceptée ou refusée | Statut à la connexion, email (si SMTP) |
| Demande de domaine ou de port | Webhook Discord admin |
| App suspendue | Webhook Discord admin |
| Node indisponible | Webhook Discord admin |

### Bases de données en un clic
Postgres, MySQL ou Redis sur le node de l'app, variables de connexion injectées automatiquement.

### Questions ouvertes
- docker-compose : quand et sous quelle forme.
- Railpack ou Nixpacks.
