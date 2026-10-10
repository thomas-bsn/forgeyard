# Documentation de Forgeyard

Forgeyard est un PaaS open source auto-hébergé : on l'installe sur son serveur, puis on déploie des conteneurs Docker depuis une interface web. Chaque app reçoit son adresse (`monapp.mondomaine.com`), son DNS et son HTTPS sans configuration à la main.

## Vue d'ensemble

Forgeyard est fait de deux programmes, issus d'un seul code Go :

- **le server** (`cmd/server`) : interface web, API, base de données, connexion des utilisateurs, DNS. Il ne parle jamais à Docker lui-même.
- **l'agent** (`cmd/agent`) : un par machine qui fait tourner des apps (un **node**). Il reçoit du server la liste des apps voulues et l'applique à Docker, puis renvoie états, métriques et logs.

```
                    Serveur principal                          Autre machine (plus tard)
          ┌─────────────────────────────────────┐            ┌──────────────────────────┐
navigateur│  server  :8080 (web, API)           │            │                          │
 ────────►│          :8081 (agents, mTLS) ◄─────┼────────────┼── agent                  │
          │            ▲                        │  connexion │     │                    │
          │            │ réseau Compose         │  ouverte   │     ▼                    │
          │  agent (node « local »)             │  par       │  Docker + Traefik + apps │
          │     │                               │  l'agent   └──────────────────────────┘
          │     ▼                               │
          │  Docker + Traefik + apps            │
          └─────────────────────────────────────┘
```

L'agent ouvre toujours la connexion vers le server : un node n'a aucun port à ouvrir pour être piloté.

## Ce qui se passe quand on crée une app

1. Le server choisit un node, enregistre l'app et crée l'enregistrement DNS `monapp.mondomaine.com` chez le fournisseur DNS.
2. Il envoie au node son **état voulu** (toutes ses apps).
3. L'agent télécharge l'image, crée le conteneur avec des labels Traefik, et lance Traefik s'il ne tourne pas encore.
4. Un visiteur ouvre `https://monapp.mondomaine.com` : le DNS mène au node, Traefik (ou votre propre reverse proxy, puis Traefik) envoie la requête au conteneur.

## L'interface

| Onglet | Qui | Contenu |
|---|---|---|
| Apps | tout le monde | Ses apps (admins : toutes, une colonne par node, avec les conteneurs externes). Page d'une app : observabilité, logs, événements, configuration. |
| Nodes | admins | Machines, leur charge, leurs apps et conteneurs ; ajout et réseau dans des fenêtres. |
| Membres | tout le monde | Les inscrits et leur profil public (bannière, description, apps publiques, activité, contact Discord). Les admins y gèrent aussi les comptes et les demandes. |
| Menu du compte (photo, en haut à droite) | tout le monde | Mon profil (photo, nom, description, email, sécurité), Réglages de l'instance (admins : Général, Domaine des apps, Connexion, Discord), thème, documentation, déconnexion. |

Chaque page a sa propre adresse (`#/apps/3`, `#/settings/domain`…).

## Sommaire

| Dossier | Contenu |
|---|---|
| [installation/](installation/README.md) | Lancer avec Docker Compose, wizard de premier lancement, mise à jour, réglages |
| [auth/](auth/README.md) | Connexion Discord ou mot de passe, demandes de compte, rôles, lien de secours |
| [nodes/](nodes/README.md) | Ajouter une machine, connexion agent ↔ server, réconciliation, métriques |
| [apps/](apps/README.md) | Créer et gérer une app, placement, états, adresse, logs en direct |
| [network/](network/README.md) | Chemin du trafic, [DNS](network/dns.md), [Traefik et reverse proxy](network/routing.md) |
| [security/](security/README.md) | Chiffrement des secrets, certificats, protections de l'API |
| [server/](server/README.md) | Organisation du code, base de données, [routes de l'API](server/api.md) |
| [roadmap.md](roadmap.md) | Ce qui est fait, ce qui reste, limites actuelles |
