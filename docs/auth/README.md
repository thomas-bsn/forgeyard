# Connexion et comptes

Code : `internal/auth`, `internal/discord`, `internal/api/{auth,discord,admin,loginlink}.go`.

## Page de connexion

Une carte centrée sous une illustration, avec l'icône et le nom de l'instance : « Continuer avec Discord » en premier, « Utiliser un identifiant » si la connexion par mot de passe est active, et un mot pour les nouveaux (se connecter avec Discord, un admin valide). Les messages de retour (demande en attente, refusée, compte désactivé…) s'affichent dans la carte.

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

Les admins sont prévenus des nouvelles demandes sur Discord (voir Notifications ci-dessous). L'email (demande acceptée ou refusée) viendra plus tard : voir la [roadmap](../roadmap.md).

## Notifications

Par webhook Discord, chiffré en base comme les autres secrets :

- **Salon des admins** (Réglages › Notifications) : nouvelles demandes de compte, suivi des demandes de bind mounts à traiter par le superadmin, demandes d'aide ([support](../support/README.md)), apps suspendues après des crashs, node hors ligne depuis une minute puis de retour ; chaque type s'active à part. Un bouton envoie un message de test.
- **Salon support** (facultatif, même page) : reçoit à la place les demandes de compte, de bind mounts et d'aide, pour les séparer des alertes.
- **Webhook personnel** (Mon profil › Notifications) : les crashs et suspensions de ses propres apps et de celles où il est [contributeur](../apps/access.md), les réponses du support et le suivi de ses demandes de bind mounts. Le superadmin y reçoit aussi les demandes à examiner et leur suivi.

Le réglage **Demandes de compte et de bind mounts** active les notifications de soumission, annulation, refus, approbation et résultat de création des demandes de bind mounts. Les messages contiennent les identifiants de la demande et de son auteur, avec un lien vers « Mes demandes » ou la file réservée au superadmin. Les configurations, chemins, variables secrètes, motifs de refus et erreurs techniques restent dans Forgeyard. Un salon partagé ne donne aucun droit de traitement aux admins ordinaires. Discord reste facultatif ; une erreur d'envoi ne change pas le résultat de la demande.

Seules les adresses `https://discord.com/api/webhooks/…` (et discordapp.com, ptb., canary.) sont acceptées. Les messages portent le nom de l'instance et un lien vers la page concernée.

## Gérer les comptes

Onglet **Membres**, vu par un admin : les demandes en attente en haut, puis tous les comptes avec leur méthode, leur rôle et leur nombre d'apps. « Gérer… » permet de :

- changer le rôle (`user` ↔ `admin`) ;
- **suspendre ses apps** : elles sont arrêtées et ni lui ni un admin ne peut les relancer avant la réactivation (qui ne les redémarre pas) ;
- **désactiver** le compte : ses sessions sont supprimées et il ne peut plus se connecter ;
- **supprimer** le compte, avec ses apps, leurs conteneurs et leurs DNS.

Le superadmin et son propre compte ne sont jamais modifiables depuis cet écran.

## Mon profil

Le menu sous la photo, en haut à droite, mène à « Mon profil » (et aux réglages de l'instance pour les admins), au thème, à la documentation et à la déconnexion.

- **Photo** : celle de Discord, mise à jour à chaque connexion et chaque jour avec le [bot](#bot-discord) (« Mettre à jour » la relit tout de suite : par le bot, sinon en refaisant la connexion Discord ; une photo changée sur Discord, qui ne charge plus, est signalée et relue toute seule avec le bot) ; ou une photo envoyée, que l'on cadre avant l'enregistrement (glisser, zoom ; le navigateur n'envoie que la zone choisie, en 256×256 ; la bannière en 1500×500) (PNG, JPEG, WebP ou GIF, 512 Ko au plus, jamais de SVG) ; sinon l'initiale.
- **Nom affiché** : celui de Discord (suivi à chaque connexion) ou un nom choisi. L'identifiant des comptes locaux ne change pas.
- **Description** (280 caractères) et **email** (prérempli avec celui de Discord, pour les notifications à venir).
- **Sécurité** : changer de mot de passe (comptes locaux ; déconnecte les autres appareils), liste des appareils connectés avec leur IP, déconnexion d'un appareil ou de tous les autres.
- **Notifications, clés SSH, jetons d'API** : à venir, la page décrit ce qu'ils feront.

## Membres et profils publics

L'onglet **Membres** liste tous les inscrits (pour les admins, avec les demandes et les actions de gestion). Chaque profil (`#/u/<id>`) montre :

- la bannière (celle de Discord, sinon sa couleur de profil, ou une image envoyée), la photo, le nom, le rôle, la date d'inscription et la description ;
- ses **apps seules** : nom, adresse et état seulement, jamais la configuration, les variables ni les logs ;
- ses **projets** : une seule ligne par projet (logo, état, nombre d'apps, adresse de son service exposé), qui ouvre la page du projet avec ses apps. C'est une vue en lecture seule : ni compose, ni variables, ni volumes, ni actions ; seules les apps que le membre affiche y apparaissent. Chacun peut masquer toutes ses apps et projets (Mon profil) ou une app à la fois (interrupteur « Visible sur le profil » sur la page de l'app) : un projet s'affiche tant qu'au moins une de ses apps est publique, et disparaît avec la dernière ;
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

## Bot Discord

Discord supprime l'ancienne photo d'un compte quand elle change : sans rien d'autre, Forgeyard garderait l'adresse de l'ancienne jusqu'à la prochaine connexion Discord du membre, et afficherait son initiale entre-temps. Le **bot** de l'application Discord règle ça (facultatif, Réglages › Discord › Bot) :

1. sur discord.com/developers/applications, la même application, onglet **Bot** : **Reset Token** ;
2. coller le token dans Forgeyard. Il est vérifié auprès de Discord, puis chiffré ; jamais réaffiché.

Le bot n'a besoin d'aucune permission ni d'être sur un serveur : il ne fait que lire les profils publics (`GET /users/{id}`). Forgeyard relit alors la photo, la bannière, la couleur et le nom Discord de chaque membre une fois par jour (et tout de suite après l'enregistrement du token), quelques comptes par seconde ; l'email ne change pas (le bot ne le voit pas). Dans Mon profil, « Mettre à jour » passe par le bot, et une photo qui ne charge plus est relue d'office.

Code : `internal/api/discordbot.go`.
