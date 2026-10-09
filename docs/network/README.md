# Réseau

Comment une requête arrive jusqu'à une app.

- [DNS](dns.md) : Forgeyard crée l'enregistrement de chaque app chez votre fournisseur.
- [Traefik et reverse proxy](routing.md) : qui reçoit le trafic sur le node et l'envoie au bon conteneur.

## Le chemin d'une requête

**Forgeyard gère les ports 80/443** (mode `traefik`) :

```
navigateur ─► DNS : monapp.domaine.com = IP du node ─► Traefik (443, HTTPS Let's Encrypt) ─► conteneur
```

**Derrière votre reverse proxy** (mode `proxy`, ex. Caddy) :

```
navigateur ─► DNS ─► votre box ─► Caddy (443, HTTPS) ─► Traefik (IP_LOCALE:8090, HTTP) ─► conteneur
```

Dans les deux cas Traefik choisit le conteneur d'après le nom demandé (`Host`), et les conteneurs des apps sont sur le réseau Docker `forgeyard`, jamais exposés directement (sauf apps sans domaine, voir [apps](../apps/README.md#adresse)).

## Ports

| Port | Où | Rôle |
|---|---|---|
| 8080 | server | Interface web et API |
| 8081 | server | Connexion des agents (mTLS). Doit rester joignable directement. |
| 80 / 443 | node en mode `traefik` | Traefik |
| 8090 (réglable) | node en mode `proxy` | Traefik en HTTP, pour votre proxy |
