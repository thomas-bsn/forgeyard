# Traefik et reverse proxy

Chaque node fait tourner un Traefik géré par l'agent (`forgeyard-traefik`, image `traefik:v3.6`). Il lit les labels des conteneurs d'apps via le socket Docker (lecture seule) : chaque version d'une app a un routeur ``Host(`<nom>.<domaine>`)`` vers son port, de priorité plus haute que la version précédente (c'est ce qui permet le déploiement sans coupure). Rien n'est écrit dans des fichiers de configuration.

Traefik n'existe que si le node a au moins une app.

## Qui gère les ports 80 et 443 ?

Une seule question, posée dans le wizard pour la machine de Forgeyard, et réglable par node dans Nodes › « Réseau… ».

### Forgeyard (mode `traefik`)

Traefik prend les ports 80 et 443, redirige HTTP vers HTTPS et obtient un certificat Let's Encrypt par app (challenge TLS-ALPN, stocké dans le volume `forgeyard-traefik-acme`). Rien à configurer.

### Mon reverse proxy (mode `proxy`)

Votre proxy (Caddy, Nginx…) garde 80 et 443. Traefik écoute en HTTP sur un port de la machine (8090 par défaut ; 8080 et 8081 sont refusés), et votre proxy lui envoie tous les sous-domaines.

Avant d'activer la machine, l'agent local teste si quelque chose répond sur les ports 80/443 de l'hôte : si oui, le wizard propose ce mode.

À ajouter **une seule fois** dans le Caddyfile, avec l'IP locale de la machine (pas 127.0.0.1). Forgeyard la trouve lui-même et l'affiche dans le wizard et dans « Réseau… » (un lien permet d'en utiliser une autre, par exemple l'IP Tailscale, sans rien enregistrer) :

- agent installé directement sur la machine : l'IP source de la route par défaut ;
- agent dans un conteneur (cas de Docker Compose) : il ne voit que son propre réseau, alors il lance quelques secondes sa propre image sur le réseau de l'hôte (`forgeyard-host-ip`, supprimé aussitôt) pour la lire.

```
*.mondomaine.com {
    reverse_proxy 192.168.1.10:8090
}
```

Le même bloc marche que Caddy tourne dans Docker ou directement sur la machine : l'IP locale est joignable des deux côtés. Ensuite chaque nouvelle app marche sans retoucher Caddy.

Les blocs plus précis (`forgeyard.mondomaine.com`, vos autres sites) passent avant le wildcard.

Le certificat wildcard demande un Caddy avec le module DNS de votre fournisseur (`acme_dns`). Sans ce module, utilisez les certificats à la demande :

```
{
    on_demand_tls {
        ask http://192.168.1.10:8080/api/caddy/ask
    }
}

https:// {
    tls {
        on_demand
    }
    reverse_proxy 192.168.1.10:8090
}
```

`/api/caddy/ask?domain=…` répond 200 seulement pour l'adresse de Forgeyard et les apps qui existent : personne ne peut faire générer des certificats pour n'importe quel nom.

Quel que soit le proxy, la règle est la même : envoyer `*.mondomaine.com` vers `http://IP_LOCALE:8090`. La fenêtre « Réseau… » l'affiche avec l'IP et le domaine remplis, et donne l'exemple prêt à copier pour Caddy, Nginx, Traefik (configuration dynamique) et Nginx Proxy Manager.

## Les modes d'entrée d'un node

La machine de Forgeyard (node A) reçoit ses visites de deux façons : **Traefik** prend les ports 80/443 et gère le HTTPS, ou **ton reverse proxy** les garde et envoie les apps à Traefik.

Les autres nodes ont trois modes, choisis dans leur fenêtre « Réseau… » :

| Mode | Les visites arrivent… | DNS des apps | Pour |
|---|---|---|---|
| **Relais par le node A** | sur le node A, dont le Traefik les passe au Traefik du node par le réseau local | IP du node A | un node sur le même réseau que le node A (derrière la même box) |
| **Directement** | sur le node, dont le Traefik prend 80/443 et gère le HTTPS | IP publique du node | un node avec sa propre IP publique (VPS, autre box) |
| **Son propre reverse proxy** | sur un Caddy ou un Nginx devant le node, qui les envoie à son Traefik | IP publique du node | un node d'un autre réseau, avec un proxy déjà en place |

En relais :

- le Traefik du node écoute en HTTP sur son port d'entrée (8090 par défaut ; pratique aussi quand le port 80 est déjà pris, par Pi-hole par exemple) ;
- le Traefik du node A reçoit, pour chaque app du node, une règle ``Host(`<app>.<domaine>`)`` vers `http://<IP locale du node>:<port>` ; l'agent l'écrit dans un fichier de configuration du conteneur Traefik (`/forgeyard/relays.yml`), que Traefik relit seul ; les règles suivent les apps, les nodes et leurs IP locales ;
- il faut que le node A joigne le node à son IP locale (même réseau, ou un VPN).

Une box n'envoie les ports 80 et 443 qu'à une seule machine : un node en « Directement » sans IP publique à lui (donc celle du node A) ne reçoit rien ; sa carte et sa fenêtre Réseau le signalent, et l'onglet Réseau de ses apps le montre en rouge. Avant que le relais soit un mode à part, il était deviné (un node derrière un proxy avec l'IP du node A) : ces nodes sont passés en « Relais » à la mise à jour.

## Pourquoi Forgeyard ne modifie pas votre proxy

La configuration de votre proxy ne change jamais : le wildcard envoie tout à Traefik, et c'est Traefik que Forgeyard met à jour. Forgeyard n'a donc besoin d'aucun accès à votre proxy et ne peut pas casser vos autres sites.

## L'adresse de Forgeyard

Réglée dans le wizard puis dans Réglages › Général (`public_url`), avec le nom de l'instance. Elle sert pour le retour Discord, les commandes d'ajout de node, le lien de secours et `/api/caddy/ask`. Le port 8081 doit rester joignable directement : avec Cloudflare, l'enregistrement de Forgeyard doit être « DNS only ».
