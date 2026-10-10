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

Si le superadmin a choisi Discord, la connexion par mot de passe est **coupée** : le formulaire disparaît de la page de login. Seul le superadmin peut la réactiver, avec l'interrupteur de Réglages › Connexion. Il ne peut la couper que si son propre compte utilise Discord et que Discord est configuré, pour ne jamais s'enfermer dehors.

## Sessions

- Cookie `forgeyard_session` (HttpOnly, SameSite=Lax, Secure en HTTPS), valable 30 jours, non prolongé à l'usage.
- La base ne garde que le **hash SHA-256** du token : une fuite de la base ne donne pas de session valide.
- Un compte désactivé perd ses sessions à la requête suivante. Les sessions expirées sont purgées toutes les heures.

## Demandes de compte

1. Un compte Discord inconnu se connecte : une **demande** est créée (ou rafraîchie), et il voit « demande en attente ».
2. Un admin l'accepte en choisissant le rôle (`user` ou `admin`), ou la refuse avec un motif facultatif.
3. Acceptée : le compte est créé, la personne se connecte normalement. Refusée : elle voit « refusée » à chaque tentative.

Pas encore fait : notifications (webhook Discord, email). Voir la [roadmap](../roadmap.md).

## Gérer les comptes

Onglet **Utilisateurs** (admins) : les demandes en attente en haut, puis tous les comptes avec leur méthode, leur rôle et leur nombre d'apps. « Gérer… » permet de :

- changer le rôle (`user` ↔ `admin`) ;
- **suspendre ses apps** : elles sont arrêtées et ni lui ni un admin ne peut les relancer avant la réactivation (qui ne les redémarre pas) ;
- **désactiver** le compte : ses sessions sont supprimées et il ne peut plus se connecter ;
- **supprimer** le compte, avec ses apps, leurs conteneurs et leurs DNS.

Le superadmin et son propre compte ne sont jamais modifiables depuis cet écran.

## Mon profil

Le menu sous la photo, en haut à droite, mène à « Mon profil » (et aux réglages de l'instance pour les admins), au thème, à la documentation et à la déconnexion.

- **Photo** : celle de Discord, mise à jour à chaque connexion (« Mettre à jour depuis Discord » refait la connexion Discord pour la récupérer tout de suite) ; ou une photo envoyée, recadrée en 256×256 par le navigateur (PNG, JPEG, WebP ou GIF, 512 Ko au plus, jamais de SVG) ; sinon l'initiale.
- **Nom affiché** : celui de Discord (suivi à chaque connexion) ou un nom choisi. L'identifiant des comptes locaux ne change pas.
- **Description** (280 caractères) et **email** (prérempli avec celui de Discord, pour les notifications à venir).
- **Sécurité** : changer de mot de passe (comptes locaux ; déconnecte les autres appareils), liste des appareils connectés avec leur IP, déconnexion d'un appareil ou de tous les autres.
- **Notifications, clés SSH, jetons d'API** : à venir, la page décrit ce qu'ils feront.

## Membres et profils publics

L'onglet **Membres** liste tous les inscrits (pour les admins, avec les demandes et les actions de gestion). Chaque profil (`#/u/<id>`) montre :

- la bannière (celle de Discord, sinon sa couleur de profil, ou une image envoyée), la photo, le nom, le rôle, la date d'inscription et la description ;
- les **apps publiques** du membre : nom, adresse et état seulement, jamais la configuration, les variables ni les logs. Chacun peut masquer toutes ses apps (Mon profil) ou une seule (interrupteur « Visible sur le profil » sur la page de l'app) ;
- leur **activité récente** (déploiements, mises en ligne, crashs de ces apps) ;
- **« Écrire sur Discord »** (ouvre son profil Discord) et son email s'il a choisi de l'afficher.

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
