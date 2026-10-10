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

## Plusieurs nodes derrière la même box

Derrière une seule IP publique, la box envoie tout le trafic web à une seule machine : celle de Forgeyard (son Traefik, ou votre proxy puis son Traefik). Les apps des autres nodes de la même IP y sont donc **relayées** :

- ces nodes se mettent en mode « Mon reverse proxy » (Traefik en HTTP sur leur port d'entrée, 8090 par défaut ; pratique aussi quand le port 80 est déjà pris, par Pi-hole par exemple) ;
- le Traefik de la machine de Forgeyard reçoit, pour chacune de leurs apps, une règle ``Host(`<app>.<domaine>`)`` vers `http://<IP locale du node>:<port>` ; le Traefik du node la sert ensuite à son app ;
- l'agent écrit ces règles dans un fichier de configuration du conteneur Traefik (`/forgeyard/relays.yml`), que Traefik relit seul ; elles suivent les apps, les nodes et leurs IP locales.

Un node avec sa propre IP publique (un VPS) n'est pas relayé : son DNS pointe sur lui et il gère ses ports 80/443 ou son propre proxy. La page Nodes signale un node derrière la même box resté en mode « Forgeyard gère les ports 80 et 443 ».

## Pourquoi Forgeyard ne modifie pas votre proxy

La configuration de votre proxy ne change jamais : le wildcard envoie tout à Traefik, et c'est Traefik que Forgeyard met à jour. Forgeyard n'a donc besoin d'aucun accès à votre proxy et ne peut pas casser vos autres sites.

## L'adresse de Forgeyard

Réglée dans le wizard puis dans Réglages › Général (`public_url`), avec le nom de l'instance. Elle sert pour le retour Discord, les commandes d'ajout de node, le lien de secours et `/api/caddy/ask`. Le port 8081 doit rester joignable directement : avec Cloudflare, l'enregistrement de Forgeyard doit être « DNS only ».
