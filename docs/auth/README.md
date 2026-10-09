# Connexion et comptes

Code : `internal/auth`, `internal/discord`, `internal/api/{auth,discord,admin,loginlink}.go`.

## Deux méthodes, une par compte

Chaque compte a **une seule** méthode : Discord **ou** identifiant / mot de passe.

### Discord

- Chaque installation enregistre sa propre application sur le portail développeur Discord (Client ID et Secret, saisis dans le wizard ou dans Réglages). Les utilisateurs n'ont rien à créer : ils cliquent sur « Se connecter avec Discord ».
- Scopes demandés : `identify` et `email`. Les comptes sont identifiés par leur ID Discord, jamais par le pseudo.
- L'adresse de retour est `<adresse de Forgeyard>/api/auth/discord/callback` : elle doit être déclarée à l'identique dans l'app Discord.
- Le paramètre `state` d'OAuth est gardé 10 minutes en mémoire et lié au navigateur par le cookie `forgeyard_oauth_state` : un lien de retour volé ne marche pas dans un autre navigateur.

### Identifiant et mot de passe

- Hash **argon2id** (19 MiB, 2 passes). Un identifiant inconnu est quand même comparé à un faux hash, pour ne pas révéler par le temps de réponse quels comptes existent.
- 10 échecs par IP en 15 minutes, puis refus (429) jusqu'à la fin de la fenêtre.

### Connexion par mot de passe désactivée après un setup Discord

Si le superadmin a choisi Discord, la connexion par mot de passe est **coupée** : le formulaire disparaît de la page de login. Seul le superadmin peut la réactiver (Réglages). Il ne peut la couper que si son propre compte utilise Discord et que Discord est configuré, pour ne jamais s'enfermer dehors.

## Sessions

- Cookie `forgeyard_session` (HttpOnly, SameSite=Lax, Secure en HTTPS), valable 30 jours, non prolongé à l'usage.
- La base ne garde que le **hash SHA-256** du token : une fuite de la base ne donne pas de session valide.
- Un compte désactivé perd ses sessions à la requête suivante. Les sessions expirées sont purgées toutes les heures.

## Demandes de compte

1. Un compte Discord inconnu se connecte : une **demande** est créée (ou rafraîchie), et il voit « demande en attente ».
2. Un admin l'accepte en choisissant le rôle (`user` ou `admin`), ou la refuse avec un motif facultatif.
3. Acceptée : le compte est créé, la personne se connecte normalement. Refusée : elle voit « refusée » à chaque tentative.

Pas encore fait : notifications (webhook Discord, email), gestion des comptes après coup (liste, changement de rôle, désactivation). Voir la [roadmap](../roadmap.md).

## Rôles

| Rôle | Peut |
|---|---|
| `superadmin` | Tout. C'est le compte créé par le wizard. Seul à pouvoir changer le domaine et la connexion par mot de passe. Ne peut pas être accordé par une demande. |
| `admin` | Voir et gérer toutes les apps, gérer les nodes, traiter les demandes de compte, configurer Discord. |
| `user` | Gérer ses propres apps. Celles des autres renvoient 404. |

## Lien de secours

Pour ne jamais être bloqué dehors (Discord en panne, mot de passe oublié) :

```bash
docker exec forgeyard forgeyard admin-login
```

La commande affiche un lien de connexion admin **à usage unique**, valable **15 minutes** (seul son hash est stocké).
